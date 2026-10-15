package handy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"syncplayer/internal/script"
	"testing"
	"time"
)

func TestHSSPLifecycle(t *testing.T) {
	var mu sync.Mutex
	var operations []string
	uploads := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		mu.Lock()
		operations = append(operations, r.URL.Path)
		mu.Unlock()
		if r.URL.Path != "/upload" && r.Header.Get("X-Connection-Key") != "private-test-key" {
			t.Error("missing key header")
		}
		switch r.URL.Path {
		case "/connected":
			io.WriteString(w, `{"connected":true}`)
		case "/servertime":
			json.NewEncoder(w).Encode(map[string]int64{"serverTime": time.Now().UnixMilli()})
		case "/upload":
			if r.Header.Get("X-Connection-Key") != "" {
				t.Error("key leaked to upload")
			}
			f, _, e := r.FormFile("file")
			if e != nil {
				t.Error(e)
				http.Error(w, "bad", 400)
				return
			}
			defer f.Close()
			b, _ := io.ReadAll(f)
			if !strings.Contains(string(b), "1000,100") {
				t.Error("not the action script")
			}
			mu.Lock()
			uploads++
			mu.Unlock()
			io.WriteString(w, `{"url":"https://scripts01.handyfeeling.com/temp/test.csv"}`)
		case "/hssp/setup":
			var p map[string]string
			json.NewDecoder(r.Body).Decode(&p)
			if len(p["sha256"]) != 64 {
				t.Error("missing digest")
			}
			io.WriteString(w, `{"result":1}`)
		case "/hssp/play":
			var p map[string]int64
			json.NewDecoder(r.Body).Decode(&p)
			if p["estimatedServerTime"] <= 0 || p["startTime"] < 0 {
				t.Error("bad sync anchor")
			}
			io.WriteString(w, `{"result":0}`)
		default:
			io.WriteString(w, `{"result":0}`)
		}
	}))
	defer ts.Close()
	c := New()
	c.base = ts.URL
	c.upload = ts.URL + "/upload"
	defer c.Close()
	if e := c.Connect("private-test-key"); e != nil {
		t.Fatal(e)
	}
	s := script.Script{Actions: []script.Action{{At: 0, Pos: 0}, {At: 1000, Pos: 100}, {At: 2000, Pos: 0}}}
	if e := c.Tick(s, 0, 1, true); e != nil {
		t.Fatal(e)
	}
	if c.playing {
		t.Fatal("setup must wait for a fresh playback timestamp")
	}
	if e := c.Tick(s, 100, 1, true); e != nil {
		t.Fatal(e)
	}
	if !c.playing {
		t.Fatal("no synchronized play")
	}
	if e := c.Tick(s, 101, 1, true); e != nil {
		t.Fatal(e)
	}
	if e := c.Stop(); e != nil {
		t.Fatal(e)
	}
	mu.Lock()
	defer mu.Unlock()
	if uploads != 1 {
		t.Fatal("script repeatedly uploaded")
	}
	joined := strings.Join(operations, ",")
	if strings.Count(joined, "/hssp/play") != 1 || !strings.Contains(joined, "/hssp/stop") {
		t.Fatal("play/stop state machine", joined)
	}
}

func TestRetimedScriptAndErrors(t *testing.T) {
	s := script.Script{Actions: []script.Action{{At: 0, Pos: 10}, {At: 1000, Pos: 90}}}
	b, e := csv(s, 2)
	if e != nil || !strings.Contains(string(b), "500,90") {
		t.Fatal("speed conversion", e)
	}
	if _, e = csv(s, 0); e == nil {
		t.Fatal("invalid speed accepted")
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"error":{"code":2001,"message":"private-test-key"}}`)
	}))
	defer ts.Close()
	c := New()
	c.base = ts.URL
	defer c.Close()
	if e := c.Connect("private-test-key"); e == nil || strings.Contains(e.Error(), "private-test-key") {
		t.Fatal("service error must be surfaced without leaking secrets")
	}
	if c.State().Connected {
		t.Fatal("failed handshake reported connected")
	}
}
