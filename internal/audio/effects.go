package audio

import (
	"math"
	"sync"

	"voxmesh/internal/config"
)

// FilterSettings es la configuración de una cadena de efectos (envío o salida por emisor). Se arma
// con InputSettings / OutputSettings (settings.go); el valor cero de cada campo es "apagado".
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
	NotchQ              float32 // 0 = defaultNotchQ
	CompressorEnabled   bool
	CompressorThreshold float32
	CompressorRatio     float32
	CompressorAttackMS  float32
	CompressorReleaseMS float32
	ExpanderEnabled     bool
	ExpanderThreshold   float32
	ExpanderRatio       float32
	ExpanderAttackMS    float32
	ExpanderReleaseMS   float32
	LimiterEnabled      bool
	LimiterThreshold    float32
	VADHoldMS           int
	GateHoldMS          int
	// RNNoiseDryMix es la parte de señal original que se suma a la salida de RNNoise (0 = solo
	// filtrada). Es 1 − la intensidad de la UI, así el valor cero es el comportamiento normal.
	RNNoiseDryMix    float32
	ShoutEnabled     bool
	ShoutThresholdDB float32 // RMS de entrada (dBFS) que activa el modo grito
	ShoutOnClip      bool    // un pico ≥ config.AudioClipPeak también lo activa
	ShoutDryMix      float32 // parte de señal original mientras dura el modo grito
	ShoutHoldMS      int
	// Final de la cadena de envío.
	SendCeilingDB float32 // techo del limitador de envío (0 = full scale)
	SendReleaseMS float32
	PreRollFrames int // frames que se mandan antes del que abre la compuerta
	FadeInSamples int // fade-in del primero de ellos
}

const defaultNotchQ = 10

type Effects struct {
	mu                    sync.Mutex
	highInput, highOutput float64
	lowOutput             float64
	notchX1, notchX2      float64
	notchY1, notchY2      float64
	compEnv, expEnv       float64 // envolventes del compresor y el expansor (lineal, 0–1)
	vadHoldFrames         int
	gateHoldFrames        int
	wasOpen               bool

	// Estado del envío (solo lo usa la cadena saliente, ver PrepareOutgoingFrames).
	sendLimiter      peakLimiter
	sendLimiterReady bool
	sendReleaseMS    float32
	preRoll          [config.AudioMaxPreRollFrames][]int16 // frames procesados que no salieron (compuerta cerrada)
	preRollCount     int
	out              [][]int16

	// Mezcla seca y modo grito (shout.go).
	dryDelay    []int16 // línea de retardo de la entrada de RNNoise (Delay() muestras)
	dryPos      int
	dry         []int16 // entrada alineada con la salida de RNNoise del bloque actual
	dryMix      float64 // proporción de original aplicada al final del bloque anterior
	shoutFrames int     // frames que le quedan al modo grito
	shouting    bool    // el último frame procesado estaba en modo grito (Shouting)
	vad         float32 // probabilidad de voz del último frame (VAD); -1 sin RNNoise
}

func (e *Effects) Reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.highInput, e.highOutput, e.lowOutput = 0, 0, 0
	e.notchX1, e.notchX2, e.notchY1, e.notchY2 = 0, 0, 0, 0
	e.compEnv, e.expEnv = 0, 0
	e.vadHoldFrames, e.gateHoldFrames, e.wasOpen = 0, 0, false
	e.sendLimiterReady, e.preRollCount = false, 0
	clear(e.dryDelay)
	e.dryPos, e.dryMix, e.shoutFrames = 0, 0, 0
	e.shouting, e.vad = false, 0
}

// Shouting indica si el último frame que pasó por la cadena de envío estaba en modo grito (para
// el medidor y el avatar).
func (e *Effects) Shouting() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.shouting
}

// VAD es la probabilidad de voz (0–1) que RNNoise le dio al último frame; -1 si no corrió.
func (e *Effects) VAD() float32 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.vad
}

// setLastFrame guarda lo que la cadena decidió del último frame (para el medidor).
func (e *Effects) setLastFrame(shouting bool, vad float32) {
	e.mu.Lock()
	e.shouting, e.vad = shouting, vad
	e.mu.Unlock()
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
func (e *Effects) limitSend(samples []int16, ceiling int16, releaseMS float32) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.sendLimiterReady {
		e.sendLimiter = newPeakLimiter(float64(ceiling), config.AudioSendLimiterAttackMS, float64(releaseMS))
		e.sendLimiterReady = true
		e.sendReleaseMS = releaseMS
	}
	if releaseMS != e.sendReleaseMS {
		e.sendLimiter.releaseCoeff = smoothingCoeff(float64(releaseMS))
		e.sendReleaseMS = releaseMS
	}
	e.sendLimiter.threshold = float64(ceiling)
	e.sendLimiter.ProcessInt16(samples)
}

// gateFrames decide qué sale con la compuerta en este frame. Cerrada: nada, y el frame se
// guarda como pre-roll (los últimos settings.PreRollFrames). Al abrirse: primero el pre-roll
// (en orden) y después el actual, con fade-in en el primero de todos. Los slices devueltos son
// del Effects y valen hasta la próxima llamada. started indica que arrancó una frase nueva.
func (e *Effects) gateFrames(samples []int16, open bool, settings FilterSettings) (frames [][]int16, started bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	keep := min(max(settings.PreRollFrames, 0), len(e.preRoll))
	if !open {
		// El frame que cierra no sale (como antes); el receptor hace su propio fade a silencio.
		e.wasOpen = false
		if keep == 0 {
			e.preRollCount = 0
			return nil, false
		}
		for e.preRollCount >= keep {
			oldest := e.preRoll[0]
			copy(e.preRoll[:], e.preRoll[1:])
			e.preRoll[len(e.preRoll)-1] = oldest
			e.preRollCount--
		}
		slot := &e.preRoll[e.preRollCount]
		*slot = append((*slot)[:0], samples...)
		e.preRollCount++
		return nil, false
	}
	e.out = e.out[:0]
	if !e.wasOpen {
		// Si el pre-roll se achicó mientras la compuerta estaba cerrada, salen los más nuevos.
		skip := max(e.preRollCount-keep, 0)
		e.out = append(e.out, e.preRoll[skip:e.preRollCount]...)
		e.preRollCount = 0
		started = true
	}
	e.out = append(e.out, samples)
	if started {
		applyFadeInInt16(e.out[0], settings.FadeInSamples)
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

// dynamicsCoeffs son los coeficientes por muestra del compresor y el expansor de un bloque.
type dynamicsCoeffs struct {
	compAttack, compRelease float64
	expAttack, expRelease   float64
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
		q := float64(settings.NotchQ)
		if q <= 0 {
			q = defaultNotchQ
		}
		omega := 2 * math.Pi * frequency / SampleRate
		alpha := math.Sin(omega) / (2 * q)
		b0, b1, b2 := 1.0, -2*math.Cos(omega), 1.0
		a0, a1, a2 := 1+alpha, -2*math.Cos(omega), 1-alpha
		nB0A0 = b0 / a0
		nB1A0 = b1 / a0
		nB2A0 = b2 / a0
		nA1A0 = a1 / a0
		nA2A0 = a2 / a0
	}
	coeffs := dynamicsCoeffs{
		compAttack:  smoothingCoeff(float64(settings.CompressorAttackMS)),
		compRelease: smoothingCoeff(float64(settings.CompressorReleaseMS)),
		expAttack:   smoothingCoeff(float64(settings.ExpanderAttackMS)),
		expRelease:  smoothingCoeff(float64(settings.ExpanderReleaseMS)),
	}
	limiterLimit := 0.0
	if settings.LimiterEnabled {
		limiterLimit = math.Pow(10, float64(settings.LimiterThreshold)/20)
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	for index, sample := range samples {
		value := (float64(sample) / math.MaxInt16) * gainFactor
		// Cada filtro guarda su propia entrada como x[n-1] (no la muestra cruda: con ganancia o
		// filtros encadenados eso daba una ganancia enorme en graves).
		if settings.HighPassEnabled {
			input := value
			value = hpAlpha * (e.highOutput + input - e.highInput)
			e.highInput, e.highOutput = input, value
		}
		if settings.LowPassEnabled {
			e.lowOutput += lpAlpha * (value - e.lowOutput)
			value = e.lowOutput
		}
		if settings.NotchEnabled {
			input := value
			value = nB0A0*input + nB1A0*e.notchX1 + nB2A0*e.notchX2 - nA1A0*e.notchY1 - nA2A0*e.notchY2
			e.notchX2, e.notchX1 = e.notchX1, input
			e.notchY2, e.notchY1 = e.notchY1, value
		}
		if settings.CompressorEnabled || settings.ExpanderEnabled {
			value *= e.dynamicsGain(math.Abs(value), settings, coeffs)
		}
		value = applyLimiter(value, settings, limiterLimit)
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

// dynamicsGain avanza las envolventes con el nivel de una muestra y devuelve la ganancia del
// compresor/expansor. La envolvente (detector de pico con attack/release) hace que la ganancia
// cambie despacio: sin ella se calculaba por muestra y deformaba la onda (waveshaping).
func (e *Effects) dynamicsGain(level float64, settings FilterSettings, coeffs dynamicsCoeffs) float64 {
	gainDB := 0.0
	if settings.CompressorEnabled {
		e.compEnv = followEnvelope(e.compEnv, level, coeffs.compAttack, coeffs.compRelease)
		db := levelDB(e.compEnv)
		threshold := float64(settings.CompressorThreshold)
		if ratio := math.Max(float64(settings.CompressorRatio), 1); db > threshold {
			gainDB += (threshold - db) * (1 - 1/ratio)
		}
	}
	if settings.ExpanderEnabled {
		e.expEnv = followEnvelope(e.expEnv, level, coeffs.expAttack, coeffs.expRelease)
		db := levelDB(e.expEnv)
		threshold := float64(settings.ExpanderThreshold)
		if ratio := math.Max(float64(settings.ExpanderRatio), 1); db < threshold {
			gainDB += (db - threshold) * (ratio - 1)
		}
	}
	if gainDB == 0 {
		return 1
	}
	return math.Pow(10, gainDB/20)
}

func followEnvelope(envelope, level, attack, release float64) float64 {
	if level > envelope {
		return envelope + (level-envelope)*attack
	}
	return envelope + (level-envelope)*release
}

// levelDB pasa un nivel lineal (0–1) a dB, con piso en -120 dB.
func levelDB(level float64) float64 {
	return 20 * math.Log10(math.Max(level, 1e-6))
}

// applyLimiter es el limitador tanh por muestra (entrada con ganancia > 0, y salida).
func applyLimiter(value float64, settings FilterSettings, limit float64) float64 {
	if !settings.LimiterEnabled || limit <= 0 {
		return value
	}
	absVal := math.Abs(value)
	if absVal <= limit {
		return value
	}
	sign := 1.0
	if value < 0 {
		sign = -1.0
	}
	excess := absVal - limit
	return sign * (limit + math.Tanh(excess*1.5)*0.1*limit)
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
