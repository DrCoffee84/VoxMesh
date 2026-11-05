package screen

import "image"

// Monitor es una pantalla de la PC, en píxeles físicos del escritorio virtual.
type Monitor struct {
	Number  int    // 1, 2… de izquierda a derecha (lo que ve el usuario en el selector)
	Device  string // \\.\DISPLAY1: identifica al monitor entre capturas
	Rect    image.Rectangle
	Primary bool
}

// PickMonitor es el monitor device; si no está (se desconectó) o device es "", el principal; si
// ninguno figura como principal, el primero. monitors no puede estar vacío.
func PickMonitor(monitors []Monitor, device string) Monitor {
	primary := monitors[0]
	for _, monitor := range monitors {
		if device != "" && monitor.Device == device {
			return monitor
		}
		if monitor.Primary {
			primary = monitor
		}
	}
	return primary
}

// bgraToRGBA pasa en el lugar los píxeles BGRA de GDI a RGBA opaco: GDI deja el alfa en 0 y con
// alfa 255 el PNG sale RGB (3 bytes por píxel en vez de 4).
func bgraToRGBA(pix []byte) {
	for i := 0; i+3 < len(pix); i += 4 {
		pix[i], pix[i+2], pix[i+3] = pix[i+2], pix[i], 255
	}
}
