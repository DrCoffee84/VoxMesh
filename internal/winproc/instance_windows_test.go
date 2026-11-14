//go:build windows

package winproc

import (
	"path/filepath"
	"testing"
	"time"
)

// TestInstanceName (C21): una instalación por carpeta, sin importar el nombre del exe ni las
// mayúsculas.
func TestInstanceName(t *testing.T) {
	base := instanceName(`C:\Juegos\VoxMesh\voxmesh.exe`)
	for _, same := range []string{`c:\juegos\voxmesh\VOXMESH.EXE`, `C:\Juegos\VoxMesh\otro.exe`, `C:\Juegos\VoxMesh\.\voxmesh.exe`} {
		if got := instanceName(same); got != base {
			t.Errorf("%s: %s, quería %s (misma carpeta)", same, got, base)
		}
	}
	if got := instanceName(`C:\Juegos\VoxMesh2\voxmesh.exe`); got == base {
		t.Errorf("otra carpeta dio el mismo nombre %s", got)
	}
}

// TestSingleInstance (C21): la segunda de la misma carpeta no es la primera y su pedido de
// mostrar llega a la que estaba; otra carpeta es otra instalación.
func TestSingleInstance(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "voxmesh.exe")
	first, ok := SingleInstance(exe)
	if !ok {
		t.Fatal("la primera instancia no quedó como primera")
	}
	shown := make(chan struct{}, 1)
	first.OnShowRequest(func() { shown <- struct{}{} })

	second, ok := SingleInstance(exe)
	if ok {
		t.Fatal("la segunda instancia de la misma carpeta quedó como primera")
	}
	second.RequestShow()
	select {
	case <-shown:
	case <-time.After(2 * time.Second):
		t.Fatal("el pedido de mostrar no llegó a la primera instancia")
	}

	if _, ok := SingleInstance(filepath.Join(t.TempDir(), "voxmesh.exe")); !ok {
		t.Fatal("otra carpeta no quedó como primera")
	}
}
