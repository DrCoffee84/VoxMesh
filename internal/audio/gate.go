package audio

import "math"

// NoiseGate suppresses frames whose RMS level is below ThresholdDB.
type NoiseGate struct {
	ThresholdDB float32
}

func (g NoiseGate) Open(samples []int16) bool {
	if len(samples) == 0 {
		return false
	}
	var sum float64
	for _, sample := range samples {
		normalized := float64(sample) / math.MaxInt16
		sum += normalized * normalized
	}
	rms := math.Sqrt(sum / float64(len(samples)))
	if rms <= 0 {
		return false
	}
	levelDB := 20 * math.Log10(rms)
	return levelDB >= float64(g.ThresholdDB)
}

func (g NoiseGate) Apply(samples []int16) bool {
	if !g.Open(samples) {
		for index := range samples {
			samples[index] = 0
		}
		return false
	}
	return true
}
