//go:build windows && cgo

package ui

import (
	"syscall"
	"unsafe"
)

// Fyne 2.6.3 no expone maximizar ni consultar si la ventana está maximizada: se hace sobre el
// HWND de GLFW. Todo esto corre en el hilo de UI (dialogOwner lee el viewport).

var (
	windowUser32           = syscall.NewLazyDLL("user32.dll")
	getWindowPlacementProc = windowUser32.NewProc("GetWindowPlacement")
	showWindowProc         = windowUser32.NewProc("ShowWindow")
)

type windowPlacement struct {
	length         uint32
	flags          uint32
	showCmd        uint32
	minPosition    [2]int32
	maxPosition    [2]int32
	normalPosition [4]int32
}

const (
	swShowMinimized       = 2
	swShowMaximized       = 3
	wpfRestoreToMaximized = 0x2
)

// windowShowState indica si la ventana está maximizada (o minimizada desde maximizada, que al
// restaurarse vuelve maximizada) y si está minimizada.
func (a *App) windowShowState() (maximized, minimized bool) {
	hwnd := a.dialogOwner()
	if hwnd == 0 {
		return false, false
	}
	placement := windowPlacement{}
	placement.length = uint32(unsafe.Sizeof(placement))
	if ok, _, _ := getWindowPlacementProc.Call(hwnd, uintptr(unsafe.Pointer(&placement))); ok == 0 {
		return false, false
	}
	minimized = placement.showCmd == swShowMinimized
	maximized = placement.showCmd == swShowMaximized || (minimized && placement.flags&wpfRestoreToMaximized != 0)
	return maximized, minimized
}

func (a *App) maximizeWindow() {
	if hwnd := a.dialogOwner(); hwnd != 0 {
		showWindowProc.Call(hwnd, swShowMaximized)
	}
}
