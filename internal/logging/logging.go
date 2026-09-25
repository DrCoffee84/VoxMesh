package logging

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

var state struct {
	sync.Mutex
	logger *log.Logger
	file   *os.File
}

func Start() error {
	state.Lock()
	defer state.Unlock()
	if state.file != nil {
		return nil
	}
	root, err := Directory()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	prune(root, time.Now().Add(-48*time.Hour))
	path := filepath.Join(root, "log-"+time.Now().Format("2006-01-02")+".log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	state.file = file
	state.logger = log.New(file, "", log.LstdFlags|log.Lmicroseconds)
	return nil
}

func Directory() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "VoxMesh"), nil
}

func Errorf(format string, args ...any) {
	state.Lock()
	defer state.Unlock()
	if state.logger != nil {
		state.logger.Printf("ERROR "+format, args...)
	}
}

func Infof(format string, args ...any) {
	state.Lock()
	defer state.Unlock()
	if state.logger != nil {
		state.logger.Printf("INFO "+format, args...)
	}
}

func Panic(value any) {
	Errorf("panic: %v\n%s", value, debug.Stack())
}

func Close() {
	state.Lock()
	defer state.Unlock()
	if state.file != nil {
		_ = state.file.Close()
	}
	state.file = nil
	state.logger = nil
}

func prune(root string, before time.Time) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "log-") || filepath.Ext(entry.Name()) != ".log" {
			continue
		}
		info, err := entry.Info()
		if err == nil && info.ModTime().Before(before) {
			_ = os.Remove(filepath.Join(root, entry.Name()))
		}
	}
}

func FormatError(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("%T: %v", err, err)
}
