package audio

import (
	"math"
	"testing"
)

func TestBusLimiter_LeavesQuietSignalUntouched(t *testing.T) {
	limiter := newBusLimiter(29000, 1, 80)
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

func TestBusLimiter_PullsSustainedPeaksToThreshold(t *testing.T) {
	limiter := newBusLimiter(29000, 1, 80)
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

func TestBusLimiter_GainHasNoSteps(t *testing.T) {
	limiter := newBusLimiter(29000, 1, 80)
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
