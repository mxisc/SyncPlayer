package applayout

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePortableLayout(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "runtime"), 0700); err != nil {
		t.Fatal(err)
	}

	layout := Resolve(filepath.Join(root, "SyncPlayer.exe"))
	if layout.Root != root || layout.Data != root {
		t.Fatalf("unexpected portable layout: %#v", layout)
	}
}

func TestResolveLegacyLayout(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "build", "bin")
	if err := os.MkdirAll(data, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "runtime"), 0700); err != nil {
		t.Fatal(err)
	}

	layout := Resolve(filepath.Join(data, "SyncPlayer.exe"))
	if layout.Root != root || layout.Data != data {
		t.Fatalf("unexpected legacy layout: %#v", layout)
	}
}

func TestResolveMissingRuntimeBesideExecutable(t *testing.T) {
	root := t.TempDir()
	layout := Resolve(filepath.Join(root, "SyncPlayer.exe"))
	if layout.Root != root || layout.Data != root {
		t.Fatalf("unexpected fallback layout: %#v", layout)
	}
}
