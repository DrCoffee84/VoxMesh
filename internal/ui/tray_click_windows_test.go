//go:build windows && cgo

package ui

import "testing"

// Solo el clic izquierdo sobre el ícono abre la ventana; el derecho (el menú) y lo demás siguen en
// systray (C27).
func TestIsTrayLeftClick(t *testing.T) {
	const wmRButtonUp, wmMouseMove = 0x0205, 0x0200
	if !isTrayLeftClick(traySystrayMessage, trayLButtonUp) {
		t.Fatal("clic izquierdo en el ícono")
	}
	for _, c := range [][2]uintptr{{traySystrayMessage, wmRButtonUp}, {traySystrayMessage, wmMouseMove}, {0x0010, trayLButtonUp}} {
		if isTrayLeftClick(uint32(c[0]), c[1]) {
			t.Fatalf("mensaje %#x lParam %#x no es el clic izquierdo", c[0], c[1])
		}
	}
}
