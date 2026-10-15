package diagnostics

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"sync"
	"time"
)

var mu sync.Mutex
var file *os.File
var directory, prefix string
var part int
var size int64
var localURL = regexp.MustCompile(`http://127\.0\.0\.1:\d+/[^\s"<>]+`)

func Init() error {
	exe, e := os.Executable()
	if e != nil {
		return e
	}
	directory = filepath.Join(filepath.Dir(exe), "logs")
	if e = os.MkdirAll(directory, 0700); e != nil {
		return e
	}
	prefix = time.Now().Format("20060102-150405") + fmt.Sprintf("-%d", os.Getpid())
	if e = rotate(); e != nil {
		return e
	}
	crash, e := os.OpenFile(filepath.Join(directory, prefix+"-crash.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if e == nil {
		debug.SetCrashOutput(crash, debug.CrashOptions{})
		crash.Close()
	}
	log.SetOutput(&standardWriter{})
	log.SetFlags(0)
	Event("app.start", map[string]any{"pid": os.Getpid(), "build": "diagnostics-v1"})
	return nil
}
func rotate() error {
	if file != nil {
		file.Sync()
		file.Close()
	}
	part++
	size = 0
	name := fmt.Sprintf("%s-%03d.jsonl", prefix, part)
	var e error
	file, e = os.OpenFile(filepath.Join(directory, name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if e == nil {
		os.WriteFile(filepath.Join(directory, "latest.txt"), []byte(name), 0600)
	}
	return e
}
func Event(event string, fields map[string]any) {
	mu.Lock()
	defer mu.Unlock()
	if file == nil {
		return
	}
	record := map[string]any{"time": time.Now().Format(time.RFC3339Nano), "event": event}
	for k, v := range fields {
		if e, ok := v.(error); ok {
			if e != nil {
				v = e.Error()
			} else {
				v = nil
			}
		}
		if s, ok := v.(string); ok {
			v = localURL.ReplaceAllString(s, "[local-media]")
		}
		record[k] = v
	}
	data, e := json.Marshal(record)
	if e != nil {
		return
	}
	// Redact URLs in nested frontend snapshots as well as top-level fields.
	data = localURL.ReplaceAll(data, []byte("[local-media]"))
	data = append(data, '\n')
	if size+int64(len(data)) > 5*1024*1024 {
		if rotate() != nil {
			return
		}
	}
	n, _ := file.Write(data)
	size += int64(n)
	// Keep the last recorded operation on disk even if native code terminates
	// the process before Go shutdown handlers can run.
	file.Sync()
}
func Close() {
	Event("app.exit", nil)
	mu.Lock()
	defer mu.Unlock()
	if file != nil {
		file.Sync()
		file.Close()
		file = nil
	}
}

type standardWriter struct{}

func (*standardWriter) Write(p []byte) (int, error) {
	Event("runtime.log", map[string]any{"message": string(p)})
	return len(p), nil
}
