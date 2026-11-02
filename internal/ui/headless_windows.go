//go:build windows && !cgo

package ui

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32      = windows.NewLazySystemDLL("user32.dll")
	messageBoxW = user32.NewProc("MessageBoxW")
)

func showHeadlessWarning() {
	title, _ := windows.UTF16PtrFromString("VoxMesh no se pudo iniciar")
	message, _ := windows.UTF16PtrFromString("Este ejecutable fue compilado sin CGO_ENABLED=1, por lo que no incluye la interfaz Fyne. Instala MinGW-w64 y recompila con CGO_ENABLED=1.")
	_, _, _ = messageBoxW.Call(0, uintptr(unsafe.Pointer(message)), uintptr(unsafe.Pointer(title)), 0x10)
}
