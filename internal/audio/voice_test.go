package audio

import (
	"math"
	"math/rand"
)

// syntheticVoice arma voz sintética: pulsos glotales con vibrato y jitter, filtrados por tres
// formantes que cambian por sílaba (vocales), con sílabas de ~250 ms y pausas. Pico ~ -10 dBFS.
func syntheticVoice(seconds, f0 float64, seed int64) []float64 {
	rng := rand.New(rand.NewSource(seed))
	n := int(seconds * SampleRate)
	vowels := [][3]float64{{730, 1090, 2440}, {270, 2290, 3010}, {530, 1840, 2480}, {570, 840, 2410}, {300, 870, 2240}}
	out := make([]float64, n)
	var res [3][2]float64 // estado de los resonadores
	phase := 0.0
	syllableEnd, gain := 0, 0.0
	formants := vowels[0]
	pause := false
	for i := range n {
		if i >= syllableEnd {
			pause = rng.Float64() < 0.25
			syllableEnd = i + int((0.15+0.2*rng.Float64())*SampleRate)
			formants = vowels[rng.Intn(len(vowels))]
		}
		target := 1.0
		if pause {
			target = 0
		}
		gain += (target - gain) * 0.002 // ~10 ms de ataque/caída
		freq := f0 * (1 + 0.03*math.Sin(2*math.Pi*5*float64(i)/SampleRate))
		phase += freq / SampleRate
		source := 0.02 * rng.NormFloat64() // aspiración
		if phase >= 1 {
			phase -= 1 + 0.01*rng.NormFloat64()
			source += 1
		}
		sample := 0.0
		for k, fc := range formants {
			bw := 80.0 + 40*float64(k)
			r := math.Exp(-math.Pi * bw / SampleRate)
			a1, a2 := 2*r*math.Cos(2*math.Pi*fc/SampleRate), -r*r
			y := (1-r)*source + a1*res[k][0] + a2*res[k][1]
			res[k][1], res[k][0] = res[k][0], y
			sample += y / float64(k+1)
		}
		out[i] = sample * gain
	}
	peak := 0.0
	for _, v := range out {
		peak = math.Max(peak, math.Abs(v))
	}
	for i := range out {
		out[i] *= 0.3 * 32767 / peak
	}
	return out
}
