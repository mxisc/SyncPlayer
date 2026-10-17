//go:build portable && windows

package main

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareRepairAndPreserveSettings(t *testing.T) {
	old := payload
	defer func() { payload = old }()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	w, _ := z.Create("build/bin/SyncPlayer.exe")
	w.Write([]byte("application"))
	z.Close()
	payload = b.Bytes()
	root := t.TempDir()
	if err := prepare(root); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(root, "build/bin/settings")
	os.MkdirAll(settings, 0700)
	os.WriteFile(filepath.Join(settings, "saved"), []byte("keep"), 0600)
	os.WriteFile(filepath.Join(root, "build/bin/SyncPlayer.exe"), []byte("bad"), 0600)
	if err := prepare(root); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(root, "build/bin/SyncPlayer.exe"))
	if string(got) != "application" {
		t.Fatal("payload was not repaired")
	}
	if _, err := os.Stat(filepath.Join(settings, "saved")); err != nil {
		t.Fatal("settings removed")
	}
}
func TestRejectTraversal(t *testing.T) {
	for _, name := range []string{"../escape", "../../escape", `..\escape`, "file:stream"} {
		if _, err := targetPath(t.TempDir(), name); err == nil {
			t.Fatalf("accepted %q", name)
		}
	}
}
