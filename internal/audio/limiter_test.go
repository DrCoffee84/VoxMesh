package audio

import (
	"math"
	"testing"
)

func TestPeakLimiter_LeavesQuietSignalUntouched(t *testing.T) {
	limiter := newPeakLimiter(29000, 1, 80)
	mix := make([]int32, FrameSamples)
	for i := range mix {
		mix[i] = int32(10000 * math.Sin(2*math.Pi*440*float64(i)/SampleRate))
	}
	original := append([]int32(nil), mix...)
	limiter.Process(mix)
	for i := range mix {
		if mix[i] != original[i] {
			t.Fatalf("sample %d changed: %d -> %d", i, original[i], mix[i])
		}
	}
}

func TestPeakLimiter_PullsSustainedPeaksToThreshold(t *testing.T) {
	limiter := newPeakLimiter(29000, 1, 80)
	// Dos participantes fuertes sumados: ~54000 de pico.
	mix := make([]int32, FrameSamples*5)
	for i := range mix {
		mix[i] = int32(54000 * math.Sin(2*math.Pi*220*float64(i)/SampleRate))
	}
	limiter.Process(mix)
	// Pasado el primer frame (attack), los picos deben quedar pegados al umbral,
	// por debajo de la rodilla de softClip (30000).
	for i := FrameSamples; i < len(mix); i++ {
		if mix[i] > 29100 || mix[i] < -29100 {
			t.Fatalf("sample %d not limited: %d", i, mix[i])
		}
	}
}

func TestPeakLimiter_GainHasNoSteps(t *testing.T) {
	limiter := newPeakLimiter(29000, 1, 80)
	// Constante positiva: la salida debe variar de forma suave (sin saltos grandes).
	mix := make([]int32, FrameSamples*2)
	for i := range mix {
		mix[i] = 50000
	}
	limiter.Process(mix)
	for i := 1; i < len(mix); i++ {
		delta := mix[i] - mix[i-1]
		if delta < 0 {
			delta = -delta
		}
		if delta > 1500 {
			t.Fatalf("gain step at sample %d: %d -> %d", i, mix[i-1], mix[i])
		}
	}
}

func TestPeakLimiter_InstantAttackNeverPassesThreshold(t *testing.T) {
	limiter := newPeakLimiter(27000, 0, 60)
	// Grito a full scale que arranca de golpe: con attack 0 ni la primera muestra pasa el techo.
	samples := make([]int16, FrameSamples*3)
	for i := range samples {
		samples[i] = int16(32767 * math.Sin(2*math.Pi*300*float64(i)/SampleRate))
	}
	limiter.ProcessInt16(samples)
	for i, s := range samples {
		if s > 27000 || s < -27000 {
			t.Fatalf("sample %d passed the ceiling: %d", i, s)
		}
	}
}

func TestPeakLimiter_ReleaseIsGradual(t *testing.T) {
	limiter := newPeakLimiter(27000, 0, 60)
	loud := make([]int16, FrameSamples)
	for i := range loud {
		loud[i] = 32000
	}
	limiter.ProcessInt16(loud)
	// Después del grito, una señal moderada no vuelve a ganancia 1 de golpe (sin escalón),
	// pero tampoco queda aplastada para siempre.
	quiet := make([]int16, FrameSamples*10)
	for i := range quiet {
		quiet[i] = 10000
	}
	limiter.ProcessInt16(quiet)
	for i := 1; i < len(quiet); i++ {
		if d := int(quiet[i]) - int(quiet[i-1]); d > 50 || d < -50 {
			t.Fatalf("gain step at sample %d: %d -> %d", i, quiet[i-1], quiet[i])
		}
	}
	if last := quiet[len(quiet)-1]; last < 9900 {
		t.Fatalf("gain did not recover after release: %d", last)
	}
}
