package audio

import (
	"math"

	"voxmesh/internal/config"
)

// ShoutDuckSettings son los gritos que llegan (AGENDA F8, perfil de Salida): cada voz de red que
// pasa ThresholdDB se escucha AmountDB más baja. Lo decide quien escucha, sin importar cómo lo
// tenga configurado el que grita (no usa transport.FlagShout).
type ShoutDuckSettings struct {
	Enabled     bool
	AmountDB    float64 // cuánto se baja un grito, en dB (positivo; 0 = nada)
	ThresholdDB float64 // RMS (dBFS) del bloque desde el que una voz empieza a bajar
}

// shoutDuckFloorDB es el nivel de un bloque en silencio.
const shoutDuckFloorDB = -100

// shoutDucker baja los gritos de un emisor. Corre por bloque del callback de playback (Engine.mix),
// antes de la cadena de salida y del volumen individual. Sin asignaciones ni locks: lo toca solo
// el callback. El valor cero está listo para usar.
//
// Por bloque: RMS en dBFS → envolvente (sube con config.AudioShoutDuckAttackMS, baja al instante)
// → reducción = AmountDB × (envolvente − umbral) / AudioShoutDuckKneeDB, entre 0 y AmountDB (una
// voz que apenas pasa el umbral baja poco; un grito, todo) → la reducción se sostiene
// AudioShoutDuckHoldMS (no rebota entre sílabas) y después vuelve con AudioShoutDuckReleaseMS. La
// ganancia va en rampa lineal a lo largo del bloque, como la de SetDeafened: sin clic. Mientras
// no hay nada que bajar el bloque no se toca (bit a bit).
type shoutDucker struct {
	envelope  float64 // dBFS
	reduction float64 // dB que se bajan al final del bloque (≥ 0)
	holdLeft  float64 // segundos que le quedan al hold
	applied   float64 // reducción (dB) aplicada al final del bloque anterior
}

// process baja samples en el lugar.
func (d *shoutDucker) process(samples []int16, settings ShoutDuckSettings) {
	n := len(samples)
	if n == 0 {
		return
	}
	if !settings.Enabled || settings.AmountDB <= 0 {
		// Apagado: si estaba bajando, vuelve en rampa en este bloque; después no toca nada.
		from := d.applied
		*d = shoutDucker{}
		if from > 0 {
			rampGain(samples, dbGain(-from), 1)
		}
		return
	}

	seconds := float64(n) / SampleRate
	level := samplesRMSdB(samples)
	if level > d.envelope {
		d.envelope += (level - d.envelope) * timeCoeff(seconds, config.AudioShoutDuckAttackMS)
	} else {
		d.envelope = level
	}

	over := (d.envelope - settings.ThresholdDB) / config.AudioShoutDuckKneeDB
	target := settings.AmountDB * math.Min(math.Max(over, 0), 1)
	switch {
	case target > 0 && target >= d.reduction:
		d.reduction = target
		d.holdLeft = config.AudioShoutDuckHoldMS / 1000
	case d.holdLeft > 0:
		d.holdLeft -= seconds
		d.reduction = math.Min(d.reduction, settings.AmountDB) // "Cuánto" pudo bajar en el medio
	default:
		d.reduction += (target - d.reduction) * timeCoeff(seconds, config.AudioShoutDuckReleaseMS)
		if d.reduction-target < 0.05 {
			d.reduction = target
		}
	}

	from, to := d.applied, d.reduction
	d.applied = to
	if from == 0 && to == 0 {
		return
	}
	rampGain(samples, dbGain(-from), dbGain(-to))
}

// rampGain multiplica samples por una ganancia que va de from a to a lo largo del bloque (≤ 1:
// nunca satura).
func rampGain(samples []int16, from, to float64) {
	n := float64(len(samples))
	for i, sample := range samples {
		gain := from + (to-from)*float64(i+1)/n
		samples[i] = int16(math.Round(float64(sample) * gain))
	}
}

// samplesRMSdB es el RMS de un bloque en dBFS (shoutDuckFloorDB si es silencio).
func samplesRMSdB(samples []int16) float64 {
	var sum float64
	for _, sample := range samples {
		normalized := float64(sample) / math.MaxInt16
		sum += normalized * normalized
	}
	if sum == 0 {
		return shoutDuckFloorDB
	}
	return math.Max(10*math.Log10(sum/float64(len(samples))), shoutDuckFloorDB)
}

// timeCoeff es el coeficiente de un polo para un paso de seconds con constante de tiempo ms.
func timeCoeff(seconds, ms float64) float64 {
	if ms <= 0 {
		return 1
	}
	return 1 - math.Exp(-seconds*1000/ms)
}

// dbGain pasa dB a ganancia lineal.
func dbGain(db float64) float64 {
	return math.Pow(10, db/20)
}
