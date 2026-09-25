package audio

import (
	"math"
	"sync"
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

func (e *Effects) Apply(data []byte, settings FilterSettings) ([]byte, error) {
	samples, err := DecodePCM(data)
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for index, sample := range samples {
		value := float64(sample) / math.MaxInt16
		value *= math.Pow(10, float64(settings.GainDB)/20)
		if settings.HighPassEnabled {
			frequency := clampFrequency(settings.HighPassHz)
			rc := 1 / (2 * math.Pi * frequency)
			alpha := rc / (rc + 1/SampleRate)
			value = alpha * (e.highOutput + value - e.highInput)
			e.highInput, e.highOutput = float64(sample)/math.MaxInt16, value
		}
		if settings.LowPassEnabled {
			frequency := clampFrequency(settings.LowPassHz)
			alpha := (1 / SampleRate) / ((1 / (2 * math.Pi * frequency)) + (1 / SampleRate))
			e.lowOutput += alpha * (value - e.lowOutput)
			value = e.lowOutput
		}
		if settings.NotchEnabled {
			frequency := clampFrequency(settings.NotchHz)
			omega := 2 * math.Pi * frequency / SampleRate
			alpha := math.Sin(omega) / (2 * 10)
			b0, b1, b2 := 1.0, -2*math.Cos(omega), 1.0
			a0, a1, a2 := 1+alpha, -2*math.Cos(omega), 1-alpha
			value = (b0/a0)*value + (b1/a0)*e.notchX1 + (b2/a0)*e.notchX2 - (a1/a0)*e.notchY1 - (a2/a0)*e.notchY2
			e.notchX2, e.notchX1 = e.notchX1, float64(sample)/math.MaxInt16
			e.notchY2, e.notchY1 = e.notchY1, value
		}
		value = applyDynamics(value, settings)
		samples[index] = clampSample(value)
	}
	return EncodePCM(samples), nil
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
	if settings.LimiterEnabled {
		limit := math.Pow(10, float64(settings.LimiterThreshold)/20)
		if value > limit {
			return limit
		}
		if value < -limit {
			return -limit
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
