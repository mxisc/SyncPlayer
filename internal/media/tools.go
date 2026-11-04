package media

import (
	"path/filepath"
	"syncplayer/internal/applayout"
)

func ToolPath(name string) string {
	layout, err := applayout.Current()
	if err != nil {
		return filepath.Join("runtime", "ffmpeg", name+".exe")
	}
	return filepath.Join(layout.Root, "runtime", "ffmpeg", name+".exe")
}
