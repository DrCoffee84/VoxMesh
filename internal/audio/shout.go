package audio

import (
	"math"

	"voxmesh/internal/config"
)

// Mezcla seca y modo grito (cadena de envío, ver PrepareOutgoingFrames).
//
// RNNoise devuelve cada muestra Delay() muestras tarde (960 a 48 kHz, exactamente un frame).
// La señal original se pasa por una línea de retardo del mismo largo, así queda alineada con
// la salida filtrada y se pueden mezclar sin desfase ni eco, sin saltear nunca RNNoise.
//
// El modo grito se detecta sobre la entrada (antes de RNNoise): como la salida va un frame
// atrasada, la mezcla llega a rampear durante el frame previo al grito y el grito sale entero.
// Entra por volumen (RMS ≥ ShoutThresholdDB) o, con ShoutOnClip, porque la voz satura (un pico
// ≥ config.AudioClipPeak): alguien que habla fuerte satura antes de llegar al volumen de grito.
// Un golpe en la mesa también satura, pero es un impulso: la saturación solo cuenta con un
// factor de cresta bajo y hacen falta config.AudioShoutEnterFrames frames fuertes seguidos para
// entrar. Si no, el golpe abría el envío (el grito cuenta como voz) y salía crudo por la mezcla.

// shoutBegin guarda la entrada de RNNoise en la línea de retardo (deja en e.dry la entrada
// alineada con lo que RNNoise va a devolver para este bloque) y actualiza el modo grito.
// Devuelve si el modo grito está activo en este frame.
func (e *Effects) shoutBegin(samples []int16, delay int, settings FilterSettings) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.dryDelay) != delay {
		e.dryDelay = make([]int16, delay)
		e.dryPos = 0
	}
	if cap(e.dry) < len(samples) {
		e.dry = make([]int16, len(samples))
	}
	e.dry = e.dry[:len(samples)]
	for i, sample := range samples {
		e.dry[i] = e.dryDelay[e.dryPos]
		e.dryDelay[e.dryPos] = sample
		e.dryPos++
		if e.dryPos == delay {
			e.dryPos = 0
		}
	}

	if !settings.ShoutEnabled {
		e.shoutFrames, e.loudFrames = 0, 0
		return false
	}
	if shoutLoud(samples, settings) {
		e.loudFrames++
	} else {
		e.loudFrames = 0
	}
	if e.loudFrames >= config.AudioShoutEnterFrames || (e.loudFrames > 0 && e.shoutFrames > 0) {
		// +2: el frame actual (rampa; por el retardo de RNNoise trae el frame de entrada anterior)
		// y el siguiente, que trae este; el hold cuenta desde ahí.
		e.shoutFrames = holdFrames(settings.ShoutHoldMS) + 2
	}
	if e.shoutFrames == 0 {
		return false
	}
	e.shoutFrames--
	return true
}

// shoutMix mezcla en samples (salida de RNNoise) la señal original alineada de shoutBegin.
// La proporción de original es RNNoiseDryMix, o ShoutDryMix si es mayor y el modo grito está
// activo; cambia en rampa lineal a lo largo del frame. Sin original que mezclar no toca nada.
func (e *Effects) shoutMix(samples []int16, shout bool, settings FilterSettings) {
	e.mu.Lock()
	defer e.mu.Unlock()
	target := clampMix(settings.RNNoiseDryMix)
	if shout {
		target = math.Max(target, clampMix(settings.ShoutDryMix))
	}
	from := e.dryMix
	e.dryMix = target
	if (from == 0 && target == 0) || len(e.dry) != len(samples) {
		return
	}
	step := (target - from) / float64(len(samples))
	for i, filtered := range samples {
		mix := from + step*float64(i+1)
		samples[i] = floatToInt16(float32(float64(filtered)*(1-mix) + float64(e.dry[i])*mix))
	}
}

// shoutLoud indica si el frame de entrada cuenta como grito: RMS ≥ ShoutThresholdDB o, con
// ShoutOnClip, saturación con energía (factor de cresta ≤ AudioShoutMaxCrestDB; un golpe satura
// con un pico suelto y no cuenta).
func shoutLoud(samples []int16, settings FilterSettings) bool {
	rms := rmsDB(samples)
	if rms >= float64(settings.ShoutThresholdDB) {
		return true
	}
	if !settings.ShoutOnClip {
		return false
	}
	peak := peakAbs(samples)
	return peak >= config.AudioClipPeak &&
		20*math.Log10(float64(peak)/math.MaxInt16)-rms <= config.AudioShoutMaxCrestDB
}

func clampMix(value float32) float64 {
	return math.Min(math.Max(float64(value), 0), 1)
}

// rmsDB es el nivel RMS del bloque en dBFS (igual que el medidor de nivel de ajustes).
func rmsDB(samples []int16) float64 {
	if len(samples) == 0 {
		return math.Inf(-1)
	}
	var sum float64
	for _, sample := range samples {
		normalized := float64(sample) / math.MaxInt16
		sum += normalized * normalized
	}
	return 20 * math.Log10(math.Sqrt(sum/float64(len(samples))))
}
