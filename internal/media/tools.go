package media

import (
	"os"
	"path/filepath"
)

func ToolPath(name string) string {
	exe, _ := os.Executable()
	return filepath.Join(filepath.Dir(exe), "..", "..", "runtime", "ffmpeg", name+".exe")
}
