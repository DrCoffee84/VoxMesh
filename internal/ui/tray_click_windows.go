//go:build windows && cgo

package ui

import (
	"unsafe"

	"fyne.io/fyne/v2"
	"golang.org/x/sys/windows"

	"voxmesh/internal/logging"
)

// Clic izquierdo en el ícono de la bandeja = abrir VoxMesh (AGENDA C27). fyne.io/systray v1.11.0
// abre el menú con los dos clics y no deja cambiarlo: su ventana oculta ("SystrayClass", creada en
// el hilo principal dentro de SetSystemTrayMenu) recibe WM_USER+1 con el mensaje del mouse en
// lParam. Se le cambia el procedimiento de ventana: el clic izquierdo abre la ventana y todo lo
// demás (el derecho → menú, el cierre, la recreación del ícono) sigue en el de systray. El
// procedimiento corre en el hilo principal (los mensajes de esa ventana los despacha GLFW).

const (
	traySystrayMessage = 0x0400 + 1  // WM_USER + 1, el mensaje del ícono en systray v1.11.0
	trayLButtonUp      = 0x0202      // WM_LBUTTONUP
	gwlpWndProc        = ^uintptr(3) // GWLP_WNDPROC = -4
)

var (
	trayUser32                   = windows.NewLazySystemDLL("user32.dll")
	findWindowExProc             = trayUser32.NewProc("FindWindowExW")
	getWindowThreadProcessIDProc = trayUser32.NewProc("GetWindowThreadProcessId")
	setWindowLongPtrProc         = trayUser32.NewProc("SetWindowLongPtrW")
	callWindowProcProc           = trayUser32.NewProc("CallWindowProcW")

	trayOldWndProc uintptr // procedimiento de systray
	trayOnLeft     func()  // qué hace el clic izquierdo (solo lo escribe hookTrayLeftClick)
	trayWndProc    = windows.NewCallback(trayWindowProc)
)

// isTrayLeftClick indica si un mensaje de la ventana de systray es el clic izquierdo en el ícono.
func isTrayLeftClick(message uint32, lParam uintptr) bool {
	return message == traySystrayMessage && lParam == trayLButtonUp
}

func trayWindowProc(hwnd windows.Handle, message uint32, wParam, lParam uintptr) uintptr {
	if isTrayLeftClick(message, lParam) && trayOnLeft != nil {
		trayOnLeft()
		return 0
	}
	result, _, _ := callWindowProcProc.Call(trayOldWndProc, uintptr(hwnd), uintptr(message), wParam, lParam)
	return result
}

// hookTrayLeftClick engancha el clic izquierdo (setupTray, goroutine principal = hilo principal,
// que es el dueño de la ventana de systray). Si no la encuentra, el clic izquierdo sigue abriendo
// el menú, como antes: una línea en el log.
func (a *App) hookTrayLeftClick() {
	if trayOldWndProc != 0 {
		return
	}
	className, _ := windows.UTF16PtrFromString("SystrayClass")
	thread := windows.GetCurrentThreadId()
	var hwnd uintptr
	for {
		next, _, _ := findWindowExProc.Call(0, hwnd, uintptr(unsafe.Pointer(className)), 0)
		if next == 0 {
			logging.Errorf("Área de notificaciones: no encontré la ventana del ícono; el clic izquierdo abre el menú")
			return
		}
		hwnd = next
		if owner, _, _ := getWindowThreadProcessIDProc.Call(hwnd, 0); uint32(owner) == thread {
			break // la nuestra (otra app con systray tiene su propia ventana con esa clase)
		}
	}
	trayOnLeft = func() { fyne.Do(a.showMainWindow) }
	old, _, err := setWindowLongPtrProc.Call(hwnd, gwlpWndProc, trayWndProc)
	if old == 0 {
		trayOnLeft = nil
		logging.Errorf("Área de notificaciones: no pude enganchar el clic izquierdo: %s", logging.FormatError(err))
		return
	}
	trayOldWndProc = old
}
