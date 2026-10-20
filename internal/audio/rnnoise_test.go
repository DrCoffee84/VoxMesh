package audio

import (
	"math"
	"testing"

	"voxmesh/internal/config"
)

func TestPrepareOutgoingPCMDropsSilentFrameWithRNNoise(t *testing.T) {
	denoiser, err := NewRNNoise()
	if err != nil {
		t.Fatal(err)
	}
	raw := EncodePCM(make([]int16, RNNoiseFrameSamples))
	output, shouldSend, err := PrepareOutgoingPCM(raw, denoiser, NoiseGate{ThresholdDB: -55}, true, 0.5)
	if err != nil {
		t.Fatal(err)
	}
	if shouldSend {
		t.Fatal("silent frame should not be sent")
	}
	if output != nil {
		t.Fatal("silent frame should not produce a packet payload")
	}
}

func TestPrepareOutgoingPCMKeepsRawWhenFilterDisabled(t *testing.T) {
	denoiser, err := NewRNNoise()
	if err != nil {
		t.Fatal(err)
	}
	raw := EncodePCM([]int16{100, -100})
	output, shouldSend, err := PrepareOutgoingPCM(raw, denoiser, NoiseGate{ThresholdDB: 0}, false, 0.5)
	if err != nil {
		t.Fatal(err)
	}
	if !shouldSend || string(output) != string(raw) {
		t.Fatal("disabled filter must send unchanged PCM")
	}
}

func TestPrepareOutgoingPCMWithFilters_ShoutGoesThroughRNNoise(t *testing.T) {
	denoiser, err := NewRNNoise()
	if err != nil {
		t.Fatal(err)
	}

	settings := DefaultFilterSettings()
	settings.RNNoiseEnabled = true
	settings.VADEnabled = false
	settings.NoiseGateEnabled = false

	// Varios frames seguidos de grito a full scale: ya no hay bypass, todo pasa por
	// RNNoise y la salida nunca debe dar la vuelta (wrap-around) ni pasar el techo.
	for frame := 0; frame < 10; frame++ {
		shoutSamples := make([]int16, FrameSamples)
		for i := range shoutSamples {
			n := frame*FrameSamples + i
			shoutSamples[i] = int16(32767 * math.Sin(2*math.Pi*300*float64(n)/SampleRate))
		}
		raw := EncodePCM(shoutSamples)

		output, shouldSend, err := PrepareOutgoingPCMWithFilters(raw, denoiser, NoiseGate{}, true, 0.5, settings, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !shouldSend {
			t.Fatal("loud shout must be sent")
		}

		outSamples, err := DecodePCM(output)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range outSamples {
			if s > config.AudioSendPeakCeiling || s < -config.AudioSendPeakCeiling {
				t.Fatalf("expected shout peak under %d, got %d", config.AudioSendPeakCeiling, s)
			}
		}
	}
}

func TestFloatToInt16Saturates(t *testing.T) {
	cases := map[float32]int16{
		0:        0,
		1234.4:   1234,
		-1234.6:  -1235,
		40000:    32767,
		-40000:   -32768,
		32767.9:  32767,
		-32768.9: -32768,
	}
	for in, want := range cases {
		if got := floatToInt16(in); got != want {
			t.Fatalf("floatToInt16(%v) = %d, want %d", in, got, want)
		}
	}
	if got := floatToInt16(float32(math.NaN())); got != 0 {
		t.Fatalf("floatToInt16(NaN) = %d, want 0", got)
	}
}

func TestPrepareOutgoingPCMWithFilters_PeakCeiling(t *testing.T) {
	// Filter disabled: safety ceiling ~27000
	loudSamples := make([]int16, FrameSamples)
	for i := range loudSamples {
		loudSamples[i] = 32000
	}
	raw := EncodePCM(loudSamples)

	output, shouldSend, err := PrepareOutgoingPCMWithFilters(raw, nil, NoiseGate{}, false, 0.5, DefaultFilterSettings(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !shouldSend {
		t.Fatal("unfiltered frame must be sent")
	}

	outSamples, err := DecodePCM(output)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range outSamples {
		if s > 27000 || s < -27000 {
			t.Fatalf("expected ceiling at 27000, got %d", s)
		}
	}
}

func TestPrepareOutgoingPCMWithFilters_TanhLimiter(t *testing.T) {
	effects := &Effects{}
	settings := DefaultFilterSettings()
	settings.LimiterEnabled = true
	settings.LimiterThreshold = -6 // ~ -6 dBFS ≈ 16422
	settings.RNNoiseEnabled = false
	settings.NoiseGateEnabled = false

	// Input with high amplitude
	samples := make([]int16, FrameSamples)
	for i := range samples {
		samples[i] = 30000
	}
	raw := EncodePCM(samples)

	output, shouldSend, err := PrepareOutgoingPCMWithFilters(raw, nil, NoiseGate{}, true, 0.5, settings, effects)
	if err != nil {
		t.Fatal(err)
	}
	if !shouldSend {
		t.Fatal("frame must be sent")
	}

	outSamples, err := DecodePCM(output)
	if err != nil {
		t.Fatal(err)
	}
	// With -6dB threshold and tanh compression, peak should be well below 20000
	if outSamples[0] >= 20000 {
		t.Fatalf("expected tanh limiter to compress below 20000, got %d", outSamples[0])
	}
}
