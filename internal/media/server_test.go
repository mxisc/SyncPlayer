package media

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLargeVideoStreamingAndRanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large video.mp4")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	const size int64 = 3 << 30
	if err := file.Truncate(size); err != nil {
		file.Close()
		t.Fatal(err)
	}
	file.WriteAt([]byte("first"), 0)
	file.WriteAt([]byte("last!"), size-5)
	file.Close()
	s, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	url, err := s.Select(path)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	// A complete GET must return headers and bytes promptly without buffering
	// the 3 GB file before delivering the response. Cancel after the prefix.
	response, err := client.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	prefix := make([]byte, 5)
	_, err = io.ReadFull(response.Body, prefix)
	response.Body.Close()
	if err != nil || string(prefix) != "first" || response.StatusCode != 200 || response.ContentLength != size {
		t.Fatalf("full GET: prefix=%q length=%d status=%d err=%v", prefix, response.ContentLength, response.StatusCode, err)
	}
	for _, tc := range []struct{ value, body, contentRange string }{{"bytes=0-4", "first", "bytes 0-4/3221225472"}, {"bytes=-5", "last!", "bytes 3221225467-3221225471/3221225472"}} {
		request, _ := http.NewRequest("GET", url, nil)
		request.Header.Set("Range", tc.value)
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != 206 || string(body) != tc.body || response.Header.Get("Content-Range") != tc.contentRange {
			t.Fatalf("range %s: status=%d body=%q contentRange=%s err=%v", tc.value, response.StatusCode, body, response.Header.Get("Content-Range"), err)
		}
	}
	request, _ := http.NewRequest("HEAD", url, nil)
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 || response.ContentLength != size {
		t.Fatal("HEAD did not return actual size")
	}
	request, _ = http.NewRequest("GET", url, nil)
	request.Header.Set("Range", "bytes=3221225472-")
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 416 {
		t.Fatal("invalid range must return 416")
	}
}

func TestOnlyCurrentSelectionIsAvailable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "selected.mp4")
	os.WriteFile(path, []byte("video"), 0600)
	s, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	oldURL, err := s.Select(path)
	if err != nil {
		t.Fatal(err)
	}
	newURL, err := s.Select(path)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	for _, url := range []string{oldURL, s.baseURL + "/video", s.baseURL + "/some-other-file"} {
		response, err := client.Get(url)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 404 {
			t.Fatalf("unexpectedly served %s", url)
		}
	}
	response, err := client.Get(newURL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("current selection unavailable")
	}
}
