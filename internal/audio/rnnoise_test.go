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
