package audio

import "math"

// peakLimiter es un limitador de pico con envolvente. Lo usan el bus de mezcla int32
// (Engine) y el final de la cadena de envío (Effects).
// Un detector de pico (attack instantáneo, release lento) sigue el nivel y de ahí sale la
// ganancia objetivo; la ganancia aplicada se suaviza muestra a muestra con attack/release,
// así no hay escalones entre buffers. No tiene lookahead: con attack > 0 los primeros samples
// de un transiente pueden pasar el umbral (en el bus queda softClip como red de seguridad);
// con attack 0 la ganancia baja en la misma muestra y ningún pico pasa el umbral.
type peakLimiter struct {
	threshold    float64
	attackCoeff  float64
	releaseCoeff float64
	envelope     float64
	gain         float64
}

func newPeakLimiter(threshold, attackMS, releaseMS float64) peakLimiter {
	return peakLimiter{
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

// next avanza la envolvente con una muestra y devuelve la ganancia a aplicarle.
func (l *peakLimiter) next(sample float64) float64 {
	// Detector de pico: sube al instante y baja con el release, así la envolvente
	// no se cae en cada cruce por cero de la onda.
	level := math.Abs(sample)
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
	return l.gain
}

// Process aplica el limitador in-place sobre el bus de mezcla.
func (l *peakLimiter) Process(mix []int32) {
	for i, sample := range mix {
		if gain := l.next(float64(sample)); gain < 1 {
			mix[i] = int32(math.Round(float64(sample) * gain))
		}
	}
}

// ProcessInt16 aplica el limitador in-place sobre muestras S16.
func (l *peakLimiter) ProcessInt16(samples []int16) {
	for i, sample := range samples {
		if gain := l.next(float64(sample)); gain < 1 {
			samples[i] = int16(math.Round(float64(sample) * gain))
		}
	}
}
