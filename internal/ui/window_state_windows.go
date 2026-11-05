//go:build windows && cgo

package ui

import (
	"unsafe"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver"
	"golang.org/x/sys/windows"
)

// Fyne 2.6.3 no expone maximizar ni consultar si la ventana está maximizada: se hace sobre el
// HWND de GLFW. Todo esto corre en el hilo de UI (dialogOwner lee el viewport).

var (
	windowUser32           = windows.NewLazySystemDLL("user32.dll")
	getWindowPlacementProc = windowUser32.NewProc("GetWindowPlacement")
	showWindowProc         = windowUser32.NewProc("ShowWindow")
	isIconicProc           = windowUser32.NewProc("IsIconic")
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
	swRestore             = 9
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

// restoreWindow saca de minimizada a una ventana: en Fyne 2.6.3 ni Show ni RequestFocus la
// restauran (el visor de pantalla que se minimizó volvía a abrirse minimizado). Hilo de UI.
func restoreWindow(w fyne.Window) {
	native, ok := w.(driver.NativeWindow)
	if !ok {
		return
	}
	native.RunNative(func(context any) {
		if win, ok := context.(driver.WindowsWindowContext); ok && win.HWND != 0 {
			if iconic, _, _ := isIconicProc.Call(win.HWND); iconic != 0 {
				_, _, _ = showWindowProc.Call(win.HWND, swRestore)
			}
		}
	})
}
