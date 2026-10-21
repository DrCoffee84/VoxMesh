package audio

import (
	"math"
	"sync"

	"voxmesh/internal/config"
)

type FilterSettings struct {
	RNNoiseEnabled      bool
	VADEnabled          bool
	NoiseGateEnabled    bool
	GainDB              float32
	HighPassEnabled     bool
	HighPassHz          float32
	LowPassEnabled      bool
	LowPassHz           float32
	NotchEnabled        bool
	NotchHz             float32
	CompressorEnabled   bool
	CompressorThreshold float32
	CompressorRatio     float32
	ExpanderEnabled     bool
	ExpanderThreshold   float32
	ExpanderRatio       float32
	LimiterEnabled      bool
	LimiterThreshold    float32
	VADHoldMS           int
	GateHoldMS          int
}

func DefaultFilterSettings() FilterSettings {
	return FilterSettings{
		RNNoiseEnabled:      true,
		VADEnabled:          true,
		NoiseGateEnabled:    true,
		HighPassHz:          80,
		LowPassHz:           12000,
		NotchHz:             50,
		CompressorThreshold: -18,
		CompressorRatio:     3,
		ExpanderThreshold:   -50,
		ExpanderRatio:       2,
		LimiterEnabled:      true,
		LimiterThreshold:    -1,
		VADHoldMS:           250,
		GateHoldMS:          250,
	}
}

type Effects struct {
	mu                    sync.Mutex
	highInput, highOutput float64
	lowOutput             float64
	notchX1, notchX2      float64
	notchY1, notchY2      float64
	vadHoldFrames         int
	gateHoldFrames        int
	wasOpen               bool

	// Estado del envío (solo lo usa la cadena saliente, ver PrepareOutgoingFrames).
	sendLimiter      peakLimiter
	sendLimiterReady bool
	preRoll          [config.AudioPreRollFrames][]int16 // frames procesados que no salieron (compuerta cerrada)
	preRollCount     int
	out              [][]int16
}

func (e *Effects) KeepVoiceOpen(vadOpen, gateOpen bool, settings FilterSettings) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if vadOpen {
		e.vadHoldFrames = holdFrames(settings.VADHoldMS)
	} else if e.vadHoldFrames > 0 {
		e.vadHoldFrames--
	}
	if gateOpen {
		e.gateHoldFrames = holdFrames(settings.GateHoldMS)
	} else if e.gateHoldFrames > 0 {
		e.gateHoldFrames--
	}
	return (!settings.VADEnabled || vadOpen || e.vadHoldFrames > 0) && (!settings.NoiseGateEnabled || gateOpen || e.gateHoldFrames > 0)
}

func holdFrames(milliseconds int) int {
	if milliseconds < 0 {
		return 0
	}
	return milliseconds / 20
}

// limitSend pasa el frame por el limitador con envolvente del final de la cadena de envío.
// Corre sobre TODOS los frames, también los que no salen, para que la envolvente siga el
// tiempo real y el pre-roll ya salga limitado.
func (e *Effects) limitSend(samples []int16, ceiling int16) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.sendLimiterReady {
		e.sendLimiter = newPeakLimiter(float64(ceiling), config.AudioSendLimiterAttackMS, config.AudioSendLimiterReleaseMS)
		e.sendLimiterReady = true
	}
	e.sendLimiter.threshold = float64(ceiling)
	e.sendLimiter.ProcessInt16(samples)
}

// gateFrames decide qué sale con la compuerta en este frame. Cerrada: nada, y el frame se
// guarda como pre-roll. Al abrirse: primero el pre-roll (los últimos frames que no salieron,
// en orden) y después el actual, con fade-in en el primero de todos. Los slices devueltos son
// del Effects y valen hasta la próxima llamada. started indica que arrancó una frase nueva.
func (e *Effects) gateFrames(samples []int16, open bool) (frames [][]int16, started bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !open {
		// El frame que cierra no sale (como antes); el receptor hace su propio fade a silencio.
		if e.preRollCount == len(e.preRoll) {
			oldest := e.preRoll[0]
			copy(e.preRoll[:], e.preRoll[1:])
			e.preRoll[len(e.preRoll)-1] = oldest
			e.preRollCount--
		}
		slot := &e.preRoll[e.preRollCount]
		*slot = append((*slot)[:0], samples...)
		e.preRollCount++
		e.wasOpen = false
		return nil, false
	}
	e.out = e.out[:0]
	if !e.wasOpen {
		e.out = append(e.out, e.preRoll[:e.preRollCount]...)
		e.preRollCount = 0
		started = true
	}
	e.out = append(e.out, samples)
	if started {
		applyFadeInInt16(e.out[0], config.AudioFadeInSamples)
	}
	e.wasOpen = true
	return e.out, started
}

func applyFadeInInt16(samples []int16, rampLen int) {
	if len(samples) < rampLen {
		rampLen = len(samples)
	}
	for i := 0; i < rampLen; i++ {
		samples[i] = int16(float64(samples[i]) * float64(i) / float64(rampLen))
	}
}

// ApplyOutput aplica la cadena de salida (por emisor, en el Engine) salteándola cuando no
// cambiaría nada: sin ganancia ni filtros y con el pico del bloque bajo el umbral del limiter.
// En ese caso el audio sale bit a bit igual (pasarlo por la cadena podía mover 1 LSB por el
// redondeo a float).
func (e *Effects) ApplyOutput(samples []int16, settings FilterSettings) {
	if settings.GainDB == 0 && !settings.HighPassEnabled && !settings.LowPassEnabled && !settings.NotchEnabled &&
		!settings.CompressorEnabled && !settings.ExpanderEnabled {
		if !settings.LimiterEnabled {
			return
		}
		limit := math.Pow(10, float64(settings.LimiterThreshold)/20) * math.MaxInt16
		if float64(peakAbs(samples)) <= limit {
			return
		}
	}
	e.ApplySamples(samples, settings)
}

func peakAbs(samples []int16) int {
	peak := 0
	for _, s := range samples {
		v := int(s)
		if v < 0 {
			v = -v
		}
		if v > peak {
			peak = v
		}
	}
	return peak
}

// ApplySamples aplica la cadena de efectos directamente sobre un slice de muestras int16 sin allocations intermedias.
func (e *Effects) ApplySamples(samples []int16, settings FilterSettings) {
	if len(samples) == 0 {
		return
	}
	gainFactor := math.Pow(10, float64(settings.GainDB)/20)
	var hpAlpha float64
	if settings.HighPassEnabled {
		frequency := clampFrequency(settings.HighPassHz)
		rc := 1 / (2 * math.Pi * frequency)
		hpAlpha = rc / (rc + 1/SampleRate)
	}
	var lpAlpha float64
	if settings.LowPassEnabled {
		frequency := clampFrequency(settings.LowPassHz)
		lpAlpha = (1 / SampleRate) / ((1 / (2 * math.Pi * frequency)) + (1 / SampleRate))
	}
	var nB0A0, nB1A0, nB2A0, nA1A0, nA2A0 float64
	if settings.NotchEnabled {
		frequency := clampFrequency(settings.NotchHz)
		omega := 2 * math.Pi * frequency / SampleRate
		alpha := math.Sin(omega) / (2 * 10)
		b0, b1, b2 := 1.0, -2*math.Cos(omega), 1.0
		a0, a1, a2 := 1+alpha, -2*math.Cos(omega), 1-alpha
		nB0A0 = b0 / a0
		nB1A0 = b1 / a0
		nB2A0 = b2 / a0
		nA1A0 = a1 / a0
		nA2A0 = a2 / a0
	}
	limiterLimit := 0.0
	if settings.LimiterEnabled {
		limiterLimit = math.Pow(10, float64(settings.LimiterThreshold)/20)
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	for index, sample := range samples {
		value := (float64(sample) / math.MaxInt16) * gainFactor
		if settings.HighPassEnabled {
			value = hpAlpha * (e.highOutput + value - e.highInput)
			e.highInput, e.highOutput = float64(sample)/math.MaxInt16, value
		}
		if settings.LowPassEnabled {
			e.lowOutput += lpAlpha * (value - e.lowOutput)
			value = e.lowOutput
		}
		if settings.NotchEnabled {
			value = nB0A0*value + nB1A0*e.notchX1 + nB2A0*e.notchX2 - nA1A0*e.notchY1 - nA2A0*e.notchY2
			e.notchX2, e.notchX1 = e.notchX1, float64(sample)/math.MaxInt16
			e.notchY2, e.notchY1 = e.notchY1, value
		}
		value = applyDynamicsFast(value, settings, limiterLimit)
		samples[index] = clampSample(value)
	}
}

func clampFrequency(frequency float32) float64 {
	if frequency < 20 {
		return 20
	}
	if frequency > SampleRate/2-100 {
		return SampleRate/2 - 100
	}
	return float64(frequency)
}

func applyDynamicsFast(value float64, settings FilterSettings, limit float64) float64 {
	if value == 0 {
		return 0
	}
	// Log10 por muestra solo si algo lo usa: con compresor y expansor apagados (default) el
	// limiter no necesita el nivel en dB.
	if settings.CompressorEnabled || settings.ExpanderEnabled {
		db := 20 * math.Log10(math.Abs(value))
		if settings.CompressorEnabled && db > float64(settings.CompressorThreshold) {
			ratio := math.Max(float64(settings.CompressorRatio), 1)
			value *= math.Pow(10, (float64(settings.CompressorThreshold)+(db-float64(settings.CompressorThreshold))/ratio-db)/20)
		}
		if settings.ExpanderEnabled && db < float64(settings.ExpanderThreshold) {
			ratio := math.Max(float64(settings.ExpanderRatio), 1)
			value *= math.Pow(10, (float64(settings.ExpanderThreshold)+(db-float64(settings.ExpanderThreshold))*ratio-db)/20)
		}
	}
	if settings.LimiterEnabled && limit > 0 {
		absVal := math.Abs(value)
		if absVal > limit {
			sign := 1.0
			if value < 0 {
				sign = -1.0
			}
			excess := absVal - limit
			value = sign * (limit + math.Tanh(excess*1.5)*0.1*limit)
		}
	}
	return value
}

func clampSample(value float64) int16 {
	if value > 1 {
		value = 1
	}
	if value < -1 {
		value = -1
	}
	return int16(value * math.MaxInt16)
}
