//go:build windows

package winproc

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// Iniciar con Windows (AGENDA F12): un valor en la clave Run del usuario con la ruta del exe y
// AutostartFlag. Sin instalador ni permisos de administrador. El estado vive en el registro, no
// en la config: así el check de Ajustes siempre dice lo que va a hacer Windows.
const (
	runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`
	// Lo que se apaga desde el Administrador de tareas → Inicio queda acá, aparte de Run: un valor
	// binario con el mismo nombre cuyo primer byte impar (0x03) quiere decir "deshabilitado".
	approvedKeyPath = `Software\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\Run`
)

// autostartName es el nombre del valor en Run y en StartupApproved (los tests usan otro para no
// pisar el de verdad). Es técnico: se queda en VoxMesh aunque el nombre visible cambie (C41).
var autostartName = "VoxMesh"

// autostartCommand es lo que Windows ejecuta al iniciar sesión.
func autostartCommand(exe string) string {
	return `"` + exe + `" ` + AutostartFlag
}

// executable es la ruta real del exe que está corriendo (como el updater).
func executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("obtener ruta del ejecutable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return exe, nil
}

// Autostart dice si Windows va a abrir la app al iniciar sesión: el valor está en Run y no está
// apagado desde el Administrador de tareas. Apunte o no a este exe (RefreshAutostart lo corrige).
func Autostart() bool {
	_, on := autostartState()
	return on
}

// autostartState devuelve el comando guardado y si está prendido.
func autostartState() (command string, on bool) {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return "", false
	}
	defer key.Close()
	command, _, err = key.GetStringValue(autostartName)
	if err != nil || command == "" {
		return "", false
	}
	return command, !disabledInTaskManager()
}

// disabledInTaskManager lee StartupApproved: sin valor = habilitado.
func disabledInTaskManager() bool {
	key, err := registry.OpenKey(registry.CURRENT_USER, approvedKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer key.Close()
	data, _, err := key.GetBinaryValue(autostartName)
	return err == nil && len(data) > 0 && data[0]&1 == 1
}

// SetAutostart prende o apaga el inicio con Windows para el exe que está corriendo. Prender
// también borra el "deshabilitado" del Administrador de tareas: si no, el check no haría nada.
func SetAutostart(on bool) error {
	exe, err := executable()
	if err != nil {
		return err
	}
	return setAutostart(exe, on)
}

func setAutostart(exe string, on bool) error {
	deleteValue(approvedKeyPath)
	if !on {
		if err := deleteValue(runKeyPath); err != nil {
			return fmt.Errorf("apagar inicio con Windows: %w", err)
		}
		return nil
	}
	key, _, err := registry.CreateKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("abrir la clave Run: %w", err)
	}
	defer key.Close()
	if err := key.SetStringValue(autostartName, autostartCommand(exe)); err != nil {
		return fmt.Errorf("prender inicio con Windows: %w", err)
	}
	return nil
}

// deleteValue borra autostartName de la clave; que no exista no es error.
func deleteValue(path string) error {
	key, err := registry.OpenKey(registry.CURRENT_USER, path, registry.SET_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer key.Close()
	if err := key.DeleteValue(autostartName); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}

// RefreshAutostart, al arrancar: si el inicio con Windows está prendido y apunta a otra ruta (el
// exe se movió de carpeta), lo apunta a este. Apagado no lo toca. Devuelve si cambió algo.
func RefreshAutostart() (bool, error) {
	exe, err := executable()
	if err != nil {
		return false, err
	}
	return refreshAutostart(exe)
}

func refreshAutostart(exe string) (bool, error) {
	command, on := autostartState()
	if !on || strings.EqualFold(command, autostartCommand(exe)) {
		return false, nil
	}
	if err := setAutostart(exe, true); err != nil {
		return false, err
	}
	return true, nil
}
