//go:build windows

package winproc

import (
	"fmt"
	"os"
	"testing"

	"golang.org/x/sys/windows/registry"
)

// useTestAutostartName cambia el nombre del valor para no tocar el inicio con Windows de verdad,
// y lo borra al terminar.
func useTestAutostartName(t *testing.T) {
	t.Helper()
	old := autostartName
	autostartName = fmt.Sprintf("VoxMeshTest-%d", os.Getpid())
	t.Cleanup(func() {
		_ = deleteValue(runKeyPath)
		_ = deleteValue(approvedKeyPath)
		autostartName = old
	})
}

func TestAutostart(t *testing.T) {
	useTestAutostartName(t)
	first, moved := `C:\Juegos\voxmesh.exe`, `D:\Otra carpeta\voxmesh.exe`
	if Autostart() {
		t.Fatal("prendido antes de prenderlo")
	}
	if changed, err := refreshAutostart(first); err != nil || changed {
		t.Fatalf("apagado, refresh no tiene que tocar nada: %v %v", changed, err)
	}
	if err := setAutostart(first, true); err != nil {
		t.Fatal(err)
	}
	if command, on := autostartState(); !on || command != `"C:\Juegos\voxmesh.exe" --autostart` {
		t.Fatalf("estado = %q %v", command, on)
	}
	if changed, err := refreshAutostart(first); err != nil || changed {
		t.Fatalf("misma ruta: %v %v", changed, err)
	}
	// El exe se movió: al abrirlo desde el lugar nuevo, Run apunta ahí.
	if changed, err := refreshAutostart(moved); err != nil || !changed {
		t.Fatalf("ruta nueva: %v %v", changed, err)
	}
	if command, _ := autostartState(); command != autostartCommand(moved) {
		t.Fatalf("no se actualizó la ruta: %q", command)
	}
	// Apagado desde el Administrador de tareas: el check lo muestra apagado y refresh no lo prende.
	key, _, err := registry.CreateKey(registry.CURRENT_USER, approvedKeyPath, registry.SET_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	err = key.SetBinaryValue(autostartName, []byte{3, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	key.Close()
	if err != nil {
		t.Fatal(err)
	}
	if Autostart() {
		t.Fatal("deshabilitado en el Administrador de tareas y figura prendido")
	}
	if changed, _ := refreshAutostart(first); changed {
		t.Fatal("refresh tocó uno deshabilitado")
	}
	// Prenderlo desde el check lo vuelve a habilitar.
	if err := setAutostart(moved, true); err != nil {
		t.Fatal(err)
	}
	if !Autostart() || disabledInTaskManager() {
		t.Fatal("prenderlo no borró el deshabilitado")
	}
	if err := setAutostart(moved, false); err != nil {
		t.Fatal(err)
	}
	if Autostart() {
		t.Fatal("sigue prendido después de apagarlo")
	}
	if err := setAutostart(moved, false); err != nil {
		t.Fatalf("apagar dos veces: %v", err)
	}
}

func TestLaunchedAtLogin(t *testing.T) {
	if LaunchedAtLogin(nil) || LaunchedAtLogin([]string{"--wait-pid", "12"}) {
		t.Fatal("sin el argumento")
	}
	if !LaunchedAtLogin([]string{AutostartFlag}) {
		t.Fatal("con el argumento")
	}
}
