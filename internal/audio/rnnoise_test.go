package audio

import "testing"

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

func TestPrepareOutgoingPCMWithFilters_ShoutBypass(t *testing.T) {
	denoiser, err := NewRNNoise()
	if err != nil {
		t.Fatal(err)
	}

	// Frame with peak > 22000 (loud shout)
	shoutSamples := make([]int16, FrameSamples)
	for i := range shoutSamples {
		shoutSamples[i] = 28000
	}
	raw := EncodePCM(shoutSamples)

	settings := DefaultFilterSettings()
	settings.RNNoiseEnabled = true
	settings.NoiseGateEnabled = false

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
		if s > 24000 || s < -24000 {
			t.Fatalf("expected shout peak clamped to 24000, got %d", s)
		}
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
