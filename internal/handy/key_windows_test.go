package handy

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestSavedKeyEncryptedAndReplaced(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings", "key.bin")
	for _, key := range []string{"test-key-first", "test-key-replacement"} {
		if err := SaveKey(path, []byte(key)); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte(key)) {
			t.Fatal("key persisted as plaintext")
		}
		loaded, err := LoadKey(path)
		if err != nil || string(loaded) != key {
			t.Fatal("saved key did not round-trip")
		}
	}
	if SaveKey(path, nil) == nil {
		t.Fatal("empty value overwrote existing key")
	}
	loaded, err := LoadKey(path)
	if err != nil || string(loaded) != "test-key-replacement" {
		t.Fatal("previous key not retained")
	}
}
