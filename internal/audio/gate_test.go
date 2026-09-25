package audio

import "testing"

func TestNoiseGate(t *testing.T) {
	gate := NoiseGate{ThresholdDB: -30}
	quiet := make([]int16, 480)
	if gate.Open(quiet) {
		t.Fatal("silence must keep the gate closed")
	}
	loud := make([]int16, 480)
	for index := range loud {
		loud[index] = 12000
	}
	if !gate.Open(loud) {
		t.Fatal("loud frame must open the gate")
	}
}

func TestNoiseGateApplyMutesClosedFrame(t *testing.T) {
	gate := NoiseGate{ThresholdDB: -30}
	samples := []int16{1, -2, 3}
	if gate.Apply(samples) {
		t.Fatal("quiet frame must be rejected")
	}
	for _, sample := range samples {
		if sample != 0 {
			t.Fatalf("sample was not muted: %d", sample)
		}
	}
}
