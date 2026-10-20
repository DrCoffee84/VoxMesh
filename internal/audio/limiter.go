package audio

import "math"

// busLimiter es un limitador de pico para el bus de mezcla int32.
// Un detector de pico (attack instantáneo, release lento) sigue el nivel del bus
// y de ahí sale la ganancia objetivo; la ganancia aplicada se suaviza muestra a
// muestra con attack/release, así no hay escalones entre buffers del callback.
// No tiene lookahead: durante el attack los primeros samples de un transiente
// pueden pasar el umbral, y para eso queda softClip como red de seguridad.
type busLimiter struct {
	threshold    float64
	attackCoeff  float64
	releaseCoeff float64
	envelope     float64
	gain         float64
}

func newBusLimiter(threshold, attackMS, releaseMS float64) busLimiter {
	return busLimiter{
		threshold:    threshold,
		attackCoeff:  smoothingCoeff(attackMS),
		releaseCoeff: smoothingCoeff(releaseMS),
		gain:         1,
	}
}

// smoothingCoeff convierte una constante de tiempo en ms a un coeficiente de
// suavizado de un polo por muestra a SampleRate.
func smoothingCoeff(ms float64) float64 {
	if ms <= 0 {
		return 1
	}
	return 1 - math.Exp(-1/(ms*SampleRate/1000))
}

// Process aplica el limitador in-place sobre el bus de mezcla.
func (l *busLimiter) Process(mix []int32) {
	for i, sample := range mix {
		// Detector de pico: sube al instante y baja con el release, así la envolvente
		// no se cae en cada cruce por cero de la onda.
		level := math.Abs(float64(sample))
		if level > l.envelope {
			l.envelope = level
		} else {
			l.envelope += (level - l.envelope) * l.releaseCoeff
		}

		target := 1.0
		if l.envelope > l.threshold {
			target = l.threshold / l.envelope
		}
		coeff := l.releaseCoeff
		if target < l.gain {
			coeff = l.attackCoeff
		}
		l.gain += (target - l.gain) * coeff

		if l.gain < 1 {
			mix[i] = int32(math.Round(float64(sample) * l.gain))
		}
	}
}
