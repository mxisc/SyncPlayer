package subtitles

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResetCacheClearsAllMediaOnly(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, "subtitle-cache")
	if err := os.Mkdir(cacheDir, 0700); err != nil {
		t.Fatal(err)
	}
	files := []string{strings.Repeat("a", 64) + ".json", strings.Repeat("b", 64) + ".json.tmp", "unrelated.json"}
	for _, file := range files {
		if err := os.WriteFile(filepath.Join(cacheDir, file), []byte("{}"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	s := New(dir, dir)
	s.cache = cache{Cues: []Cue{{Original: "cached"}}}
	if err := s.ResetCache(); err != nil {
		t.Fatal(err)
	}
	remaining, err := os.ReadDir(cacheDir)
	if err != nil || len(remaining) != 1 || remaining[0].Name() != "unrelated.json" || len(s.cache.Cues) != 0 {
		t.Fatalf("unexpected cache state after reset: %v, %v", remaining, err)
	}
}
