package applayout

import (
	"os"
	"path/filepath"
)

type Layout struct {
	Root string
	Data string
}

func Resolve(executable string) Layout {
	data := filepath.Dir(executable)
	if hasRuntime(data) {
		return Layout{Root: data, Data: data}
	}

	legacyRoot := filepath.Clean(filepath.Join(data, "..", ".."))
	if hasRuntime(legacyRoot) {
		return Layout{Root: legacyRoot, Data: data}
	}

	return Layout{Root: data, Data: data}
}

func Current() (Layout, error) {
	executable, err := os.Executable()
	if err != nil {
		return Layout{}, err
	}
	return Resolve(executable), nil
}

func hasRuntime(root string) bool {
	info, err := os.Stat(filepath.Join(root, "runtime"))
	return err == nil && info.IsDir()
}
