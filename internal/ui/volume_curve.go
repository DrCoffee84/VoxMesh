//go:build cgo

package ui

import "math"

// Sliders de volumen con curva logarítmica (AGENDA C23): el oído percibe el volumen en dB, así que
// con un slider lineal el 50 % se oía apenas más bajo (−6 dB) y casi todo el cambio quedaba entre
// 0 y 20 %. La perilla se mueve en una escala de dB y se sigue guardando la ganancia real
// (PeerVolumes, soundboard_volume): un valor guardado vuelve a la misma ganancia.

const (
	// volumeFloorDB es la ganancia apenas pasado el 0 (que es silencio): −40 dB, el 1 %.
	volumeFloorDB = -40.0
	// volumeDetent es la mitad del "clic" del 100 %, en recorrido (0–1): ese tramo da exactamente
	// 100 %, así se encuentra fácil y no se guarda (como antes, 1.0 no se guarda).
	volumeDetent = 0.05
	// volumeSliderStep es el paso de los sliders de volumen (en recorrido).
	volumeSliderStep = 0.01
)

// volumeCurve pasa la posición de un slider de volumen (0–1) a ganancia y al revés. Del 0 al
// "clic" sube parejo desde volumeFloorDB hasta 0 dB; del "clic" al final, de 0 dB al máximo, con
// los mismos dB por tramo de recorrido.
type volumeCurve struct {
	maxGain   float64
	maxDB     float64
	low, high float64 // tramo del "clic" (ganancia 1)
}

// newVolumeCurve arma la curva de un slider que llega a maxGain (1 = 100 %, 3 = 300 %).
func newVolumeCurve(maxGain float64) volumeCurve {
	maxGain = math.Max(maxGain, 1)
	maxDB := 20 * math.Log10(maxGain)
	if maxDB <= 0 {
		return volumeCurve{maxGain: 1, low: 1 - volumeDetent, high: 1}
	}
	travel := 1 - 2*volumeDetent
	low := travel * -volumeFloorDB / (-volumeFloorDB + maxDB)
	return volumeCurve{maxGain: maxGain, maxDB: maxDB, low: low, high: low + 2*volumeDetent}
}

// gain es la ganancia de la posición pos.
func (c volumeCurve) gain(pos float64) float64 {
	switch {
	case pos <= 0:
		return 0
	case pos < c.low:
		return math.Pow(10, volumeFloorDB*(1-pos/c.low)/20)
	case pos <= c.high:
		return 1
	case pos >= 1:
		return c.maxGain
	default:
		return math.Pow(10, c.maxDB*(pos-c.high)/(1-c.high)/20)
	}
}

// pos es la posición de la ganancia gain (el centro del "clic" para 1). Una ganancia más baja que
// volumeFloorDB queda en el 0 (se ve ahí, pero no cambia hasta que se mueve el slider).
func (c volumeCurve) pos(gain float64) float64 {
	switch {
	case gain <= 0:
		return 0
	case math.Abs(gain-1) < 1e-9:
		return (c.low + c.high) / 2
	case gain < 1:
		return math.Max(c.low*(1-20*math.Log10(gain)/volumeFloorDB), 0)
	case c.maxDB == 0:
		return 1
	default:
		return math.Min(c.high+(1-c.high)*20*math.Log10(gain)/c.maxDB, 1)
	}
}
