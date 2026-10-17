//go:build portable && windows

package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	_ "embed"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

//go:embed payload.zip
var payload []byte

var version = "1.0.0"

func main() {
	if err := launch(); err != nil {
		message, _ := windows.UTF16PtrFromString("SyncPlayer 启动失败：\n" + err.Error())
		title, _ := windows.UTF16PtrFromString("SyncPlayer " + version)
		windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(message)), uintptr(unsafe.Pointer(title)), 0x10)
		os.Exit(1)
	}
}

func launch() error {
	cache, err := os.UserCacheDir()
	if err != nil {
		return err
	}
	root := filepath.Join(cache, "SyncPlayer", version)
	name, _ := windows.UTF16PtrFromString("Local\\SyncPlayer-Unpack-" + version)
	mutex, err := windows.CreateMutex(nil, false, name)
	if err != nil && err != windows.ERROR_ALREADY_EXISTS {
		return err
	}
	defer windows.CloseHandle(mutex)
	if _, err = windows.WaitForSingleObject(mutex, windows.INFINITE); err != nil {
		return err
	}
	err = prepare(root)
	windows.ReleaseMutex(mutex)
	if err != nil {
		return err
	}
	cmd := exec.Command(filepath.Join(root, "build", "bin", "SyncPlayer.exe"), os.Args[1:]...)
	cmd.Dir = root
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Run()
}

func prepare(root string) error {
	archive, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		return err
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(payload))
	marker := filepath.Join(root, ".payload-sha256")
	ready, _ := os.ReadFile(marker)
	complete := string(ready) == digest
	for _, file := range archive.File {
		path, err := targetPath(root, file.Name)
		if err != nil {
			return err
		}
		if file.FileInfo().IsDir() {
			continue
		}
		info, err := os.Stat(path)
		if err != nil || uint64(info.Size()) != file.UncompressedSize64 {
			complete = false
		}
	}
	if complete {
		return nil
	}
	for _, file := range archive.File {
		path, err := targetPath(root, file.Name)
		if err != nil {
			return err
		}
		if file.FileInfo().IsDir() {
			continue
		}
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		if err = unpackFile(file, path); err != nil {
			return err
		}
	}
	return os.WriteFile(marker, []byte(digest), 0600)
}

func targetPath(root, name string) (string, error) {
	path := filepath.Join(root, filepath.FromSlash(name))
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) || strings.Contains(rel, ":") {
		return "", fmt.Errorf("invalid package path")
	}
	return path, nil
}

func unpackFile(file *zip.File, path string) error {
	source, err := file.Open()
	if err != nil {
		return err
	}
	defer source.Close()
	target, err := os.CreateTemp(filepath.Dir(path), ".unpack-*")
	if err != nil {
		return err
	}
	defer os.Remove(target.Name())
	_, copyErr := io.Copy(target, source)
	closeErr := target.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(target.Name(), path)
}
