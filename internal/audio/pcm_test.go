package audio

import "testing"

func TestPreparePCMCanKeepRawAudio(t *testing.T) {
	input := EncodePCM([]int16{1, -2, 3})
	output, err := PreparePCM(input, NoiseGate{ThresholdDB: -1}, false)
	if err != nil {
		t.Fatal(err)
	}
	if string(output) != string(input) {
		t.Fatal("raw audio was modified")
	}
}

func TestPreparePCMAppliesGateByFrame(t *testing.T) {
	input := EncodePCM([]int16{1, -2, 3})
	output, err := PreparePCM(input, NoiseGate{ThresholdDB: -30}, true)
	if err != nil {
		t.Fatal(err)
	}
	samples, err := DecodePCM(output)
	if err != nil {
		t.Fatal(err)
	}
	for _, sample := range samples {
		if sample != 0 {
			t.Fatalf("filtered sample was not muted: %d", sample)
		}
	}
}
