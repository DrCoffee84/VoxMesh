package screen

import "time"

// Ritmo de captura y de pintado. Con una captura fija cada 500 ms y una pintada cada 250 ms un
// cambio tardaba en verse ~0,4 s de promedio aunque la red sobrara.
const (
	// CaptureFast es el intervalo mientras la pantalla cambia; CaptureIdle, con la pantalla quieta
	// (el BitBlt de 1080p tarda ~30 ms: ~20 % de un núcleo rápido, ~6 % quieta).
	CaptureFast = 150 * time.Millisecond
	CaptureIdle = 500 * time.Millisecond
	// IdleAfter: sin cambios durante este tiempo la captura vuelve a CaptureIdle.
	IdleAfter = 2 * time.Second

	// El visor pinta cuando dejan de llegar chunks por PaintSettle (terminó una tanda), o a los
	// PaintMaxWait de cambios seguidos, y nunca más seguido que PaintMinGap (cada pintada sube la
	// textura entera).
	PaintSettle  = 30 * time.Millisecond
	PaintMaxWait = 120 * time.Millisecond
	PaintMinGap  = 80 * time.Millisecond
)

// NextCapture es cuánto falta para la próxima captura: CaptureFast si hubo cambios hace menos de
// IdleAfter, CaptureIdle si no (o si nunca hubo).
func NextCapture(lastChange, now time.Time) time.Duration {
	if !lastChange.IsZero() && now.Sub(lastChange) < IdleAfter {
		return CaptureFast
	}
	return CaptureIdle
}

// PaintDue dice si el visor tiene que pintar ahora. dirtySince es el primer cambio sin pintar
// (cero = nada que pintar), lastChunk la llegada del último chunk y lastPaint la última pintada.
func PaintDue(dirtySince, lastChunk, lastPaint, now time.Time) bool {
	if dirtySince.IsZero() || now.Sub(lastPaint) < PaintMinGap {
		return false
	}
	return now.Sub(lastChunk) >= PaintSettle || now.Sub(dirtySince) >= PaintMaxWait
}
