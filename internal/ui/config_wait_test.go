//go:build cgo

package ui

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// newAppWithOwnConfig arma una App cuya config se guarda en una carpeta propia. Al terminar, espera
// el guardado pendiente (configSaver escribe en otra goroutine) antes de borrarla: si no, la
// escritura se cruza con el borrado, falla y su aviso llega al test siguiente (como en T3).
func newAppWithOwnConfig(t *testing.T) *App {
	t.Helper()
	dir, err := os.MkdirTemp("", "voxmesh-test")
	if err != nil {
		t.Fatal(err)
	}
	a := New(filepath.Join(dir, "voxmesh.exe"))
	t.Cleanup(func() {
		waitConfigSaved(a)
		_ = os.RemoveAll(dir)
	})
	return a
}

// waitConfigSaved espera a que no quede ningún guardado encolado ni escribiéndose.
func waitConfigSaved(a *App) {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(a.cfgSave) == 0 {
			a.cfgSaveMu.Lock()
			a.cfgSaveMu.Unlock()
			time.Sleep(20 * time.Millisecond)
			if len(a.cfgSave) == 0 {
				a.cfgSaveMu.Lock()
				a.cfgSaveMu.Unlock()
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
}
