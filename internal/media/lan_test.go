package media

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLANFeedRangeAndResolution(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sample_LR_180.mp4")
	if err := os.WriteFile(p, []byte("0123456789"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Address() != "" {
		t.Fatal("service started before requested")
	}
	if err = s.start("127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	// This fixture tests HTTP ranges, not ffprobe; the bytes are not a real video.
	s.mu.Lock()
	s.catalog = map[string]CatalogEntry{"a": {Name: filepath.Base(p), Path: p}}
	s.metadata = map[string]VideoMetadata{"a": {ID: "a", Codec: "h264", Height: 1080, Duration: 1}}
	s.mu.Unlock()
	prefix := "/" + s.token + "/"
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, httptest.NewRequest("GET", prefix, nil))
	var feed map[string]any
	if err = json.Unmarshal(rr.Body.Bytes(), &feed); err != nil || rr.Code != 200 {
		t.Fatal("invalid feed")
	}
	rr = httptest.NewRecorder()
	req := httptest.NewRequest("GET", prefix+"file/a/sample_LR_180.mp4", nil)
	req.Header.Set("Range", "bytes=2-4")
	s.ServeHTTP(rr, req)
	if rr.Code != http.StatusPartialContent || rr.Body.String() != "234" {
		t.Fatal("range failed")
	}
	raw := s.baseURL + prefix + "file/a/sample_LR_180.mp4"
	if s.Resolve(raw) != p || s.Resolve("http://other.invalid"+prefix+"file/a/sample_LR_180.mp4") != "" {
		t.Fatal("URL matching failed")
	}
	rr = httptest.NewRecorder()
	s.ServeHTTP(rr, httptest.NewRequest("GET", "/invalid/file/a/video.mp4", nil))
	if rr.Code != 404 {
		t.Fatal("unauthorized file exposed")
	}
	s.Close()
	if s.Address() != "" || s.Resolve(raw) != "" {
		t.Fatal("closed service remains available")
	}
}
func TestVideoClientDetection(t *testing.T) {
	s, err := New()
	if err != nil {
		t.Fatal(err)
	}
	s.lan = true
	for _, address := range []string{"127.0.0.1:1000", "203.0.113.1:1000"} {
		r := httptest.NewRequest("GET", "/file/a/video.mp4", nil)
		r.RemoteAddr = address
		s.recordVideoClient(r)
		if s.VideoClient() != "" {
			t.Fatal("invalid client selected")
		}
	}
	r := httptest.NewRequest("HEAD", "/file/a/video.mp4", nil)
	r.RemoteAddr = "192.168.60.10:1000"
	s.recordVideoClient(r)
	if s.VideoClient() != "" {
		t.Fatal("HEAD selected device")
	}
	r.Method = "GET"
	s.recordVideoClient(r)
	if s.VideoClient() != "192.168.60.10" {
		t.Fatal("video client not recognized")
	}
	s.Close()
	if s.VideoClient() != "" {
		t.Fatal("stale device retained")
	}
}

func TestPortalAndRealMetadataDescription(t *testing.T) {
	s, err := New()
	if err != nil {
		t.Fatal(err)
	}
	s.lan = true
	s.baseURL = "http://192.168.60.2:8787"
	s.catalog = map[string]CatalogEntry{"a": {Name: "sample_LR_180.mp4", Path: "C:/sample_LR_180.mp4"}}
	s.metadata = map[string]VideoMetadata{"a": {ID: "a", Codec: "h265", Height: 2160, Duration: 42.5}}
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, httptest.NewRequest("GET", "/web", nil))
	if !strings.Contains(rr.Header().Get("Content-Type"), "text/html") || !strings.Contains(rr.Body.String(), "deovr://http://192.168.60.2:8787/deovr") {
		t.Fatal("portal entry missing")
	}
	rr = httptest.NewRecorder()
	s.ServeHTTP(rr, httptest.NewRequest("GET", "/deovr", nil))
	var feed struct {
		Scenes []struct {
			List []struct {
				ID          int32  `json:"id"`
				VideoURL    string `json:"video_url"`
				VideoLength int32  `json:"videoLength"`
			} `json:"list"`
		} `json:"scenes"`
	}
	if err = json.Unmarshal(rr.Body.Bytes(), &feed); err != nil || len(feed.Scenes) != 1 || len(feed.Scenes[0].List) != 1 || feed.Scenes[0].List[0].VideoLength != 43 {
		t.Fatal("integer feed duration missing")
	}
	feedID := feed.Scenes[0].List[0].ID
	if feedID <= 0 || feed.Scenes[0].List[0].VideoURL != s.baseURL+"/item/a.json" {
		t.Fatal("feed must use a positive integer ID without changing file-routing IDs")
	}
	rr = httptest.NewRecorder()
	s.ServeHTTP(rr, httptest.NewRequest("GET", "/item/a.json", nil))
	var detail struct {
		VideoLength int32 `json:"videoLength"`
		ID          int32 `json:"id"`
		Encodings   []struct {
			Name    string `json:"name"`
			Sources []struct {
				Resolution int `json:"resolution"`
			} `json:"videoSources"`
		} `json:"encodings"`
	}
	if err = json.Unmarshal(rr.Body.Bytes(), &detail); err != nil || detail.Encodings[0].Name != "h265" || detail.Encodings[0].Sources[0].Resolution != 2160 {
		t.Fatal("codec/resolution missing")
	}
	if detail.VideoLength != 43 {
		t.Fatal("integer detail duration missing")
	}
	if detail.ID != feedID {
		t.Fatal("feed and detail IDs must match")
	}
}
func TestUnifiedDeoEntry(t *testing.T) {
	s, _ := New()
	s.lan = true
	s.baseURL = "http://192.168.60.2:8787"
	s.catalog = map[string]CatalogEntry{}
	s.metadata = map[string]VideoMetadata{}
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != 200 || !strings.Contains(rr.Header().Get("Content-Type"), "application/json") || rr.Header().Get("Location") != "" {
		t.Fatal("root must directly serve the native feed")
	}
	for _, route := range []string{"/deovr", "/deovr/", "/deovr.json"} {
		rr = httptest.NewRecorder()
		s.ServeHTTP(rr, httptest.NewRequest("GET", route, nil))
		if rr.Code != 200 || !strings.Contains(rr.Header().Get("Content-Type"), "application/json") {
			t.Fatalf("missing feed alias %s", route)
		}
	}
}
