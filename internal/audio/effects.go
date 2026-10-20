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

func (e *Effects) SmoothTransition(samples []int16, open bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	rampLen := 144 // ~3 ms at 48 kHz
	if len(samples) < rampLen {
		rampLen = len(samples)
	}
	if !e.wasOpen && open {
		for i := 0; i < rampLen; i++ {
			factor := float64(i) / float64(rampLen)
			samples[i] = int16(float64(samples[i]) * factor)
		}
	} else if e.wasOpen && !open {
		start := len(samples) - rampLen
		for i := 0; i < rampLen; i++ {
			factor := 1.0 - (float64(i) / float64(rampLen))
			samples[start+i] = int16(float64(samples[start+i]) * factor)
		}
	}
	e.wasOpen = open
}

func ApplyPeakCeiling(samples []int16, ceiling int16) {
	if ceiling <= 0 {
		ceiling = config.AudioSendPeakCeiling // ~ -1.7 dBFS ceiling
	}
	maxVal := int16(0)
	for _, s := range samples {
		abs := s
		if abs < 0 {
			abs = -abs
		}
		if abs > maxVal {
			maxVal = abs
		}
	}
	if maxVal > ceiling {
		scale := float64(ceiling) / float64(maxVal)
		for i := range samples {
			samples[i] = int16(float64(samples[i]) * scale)
		}
	}
}

func (e *Effects) Apply(data []byte, settings FilterSettings) ([]byte, error) {
	samples, err := DecodePCM(data)
	if err != nil {
		return nil, err
	}
	e.ApplySamples(samples, settings)
	return EncodePCM(samples), nil
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

func applyDynamics(value float64, settings FilterSettings) float64 {
	limit := 0.0
	if settings.LimiterEnabled {
		limit = math.Pow(10, float64(settings.LimiterThreshold)/20)
	}
	return applyDynamicsFast(value, settings, limit)
}

func applyDynamicsFast(value float64, settings FilterSettings, limit float64) float64 {
	level := math.Abs(value)
	if level == 0 {
		return 0
	}
	db := 20 * math.Log10(level)
	if settings.CompressorEnabled && db > float64(settings.CompressorThreshold) {
		ratio := math.Max(float64(settings.CompressorRatio), 1)
		value *= math.Pow(10, (float64(settings.CompressorThreshold)+(db-float64(settings.CompressorThreshold))/ratio-db)/20)
	}
	if settings.ExpanderEnabled && db < float64(settings.ExpanderThreshold) {
		ratio := math.Max(float64(settings.ExpanderRatio), 1)
		value *= math.Pow(10, (float64(settings.ExpanderThreshold)+(db-float64(settings.ExpanderThreshold))*ratio-db)/20)
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
