package library

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanCacheAndRefresh(t *testing.T) {
	root := t.TempDir()
	settings := filepath.Join(t.TempDir(), "library.json")
	for path, body := range map[string]string{"a/movie.mp4": "", "a/MOVIE.FUNSCRIPT": "{}", "b/voice.mp3": "", "b/source.ts": "export const x=1", "b/deep/clip.mkv": ""} {
		p := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	store := New(settings)
	state, err := store.Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Items) != 3 || state.Directories != 4 || state.Scanning {
		t.Fatalf("unexpected scan: %+v", state)
	}
	for _, item := range state.Items {
		if item.Name == "movie.mp4" && !item.HasScript {
			t.Fatal("movie script was not matched")
		}
		if item.Name == "voice.mp3" && item.Kind != "audio" {
			t.Fatal("audio kind was not detected")
		}
	}
	cached := New(settings)
	if len(cached.Snapshot().Items) != 3 {
		t.Fatal("cache not restored")
	}
	for _, item := range state.Items {
		if _, err := cached.Path(item.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(filepath.Join(root, "a/movie.mp4")); err != nil {
		t.Fatal(err)
	}
	if _, err := cached.Scan(filepath.Join(root, "missing")); err == nil {
		t.Fatal("missing root accepted")
	}
	if len(cached.Snapshot().Items) != 3 {
		t.Fatal("failed scan destroyed cache")
	}
	state, err = cached.Scan(root)
	if err != nil || len(state.Items) != 2 {
		t.Fatalf("refresh failed: %+v %v", state, err)
	}
}
