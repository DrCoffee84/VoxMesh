//go:build cgo

package audio

import "testing"

func TestSoftClip(t *testing.T) {
	// Dentro del rango lineal
	if val := softClip(0); val != 0 {
		t.Fatalf("expected 0, got %d", val)
	}
	if val := softClip(15000); val != 15000 {
		t.Fatalf("expected 15000, got %d", val)
	}
	if val := softClip(-15000); val != -15000 {
		t.Fatalf("expected -15000, got %d", val)
	}
	if val := softClip(30000); val != 30000 {
		t.Fatalf("expected 30000, got %d", val)
	}
	if val := softClip(-30000); val != -30000 {
		t.Fatalf("expected -30000, got %d", val)
	}

	// Picos altos por suma de múltiples participantes hablando a la vez
	posHigh := softClip(40000)
	if posHigh <= 30000 || posHigh > 32767 {
		t.Fatalf("expected between 30001 and 32767, got %d", posHigh)
	}

	posExtreme := softClip(100000)
	if posExtreme <= posHigh || posExtreme > 32767 {
		t.Fatalf("expected between %d and 32767, got %d", posHigh, posExtreme)
	}

	negHigh := softClip(-40000)
	if negHigh >= -30000 || negHigh < -32768 {
		t.Fatalf("expected between -30001 and -32768, got %d", negHigh)
	}

	negExtreme := softClip(-100000)
	if negExtreme >= negHigh || negExtreme < -32768 {
		t.Fatalf("expected between %d and -32768, got %d", negHigh, negExtreme)
	}
}

func TestLimitFrame(t *testing.T) {
	// Frame under threshold should be untouched
	normal := EncodePCM([]int16{5000, 10000, -10000})
	limitFrame(normal, 30000)
	samples, err := DecodePCM(normal)
	if err != nil {
		t.Fatal(err)
	}
	if samples[1] != 10000 {
		t.Fatalf("expected 10000, got %d", samples[1])
	}

	// Frame over threshold should be scaled down so peak == threshold
	loud := EncodePCM([]int16{16000, 32000, -32000})
	limitFrame(loud, 30000)
	samples, err = DecodePCM(loud)
	if err != nil {
		t.Fatal(err)
	}
	if samples[1] != 30000 {
		t.Fatalf("expected 30000, got %d", samples[1])
	}
	if samples[0] != 15000 {
		t.Fatalf("expected 15000, got %d", samples[0])
	}
}

func TestApplyPeakCeiling(t *testing.T) {
	// Peak below ceiling untouched
	under := []int16{100, 20000, -20000}
	ApplyPeakCeiling(under, 27000)
	if under[1] != 20000 {
		t.Fatalf("expected 20000, got %d", under[1])
	}

	// Peak above ceiling scaled
	over := []int16{16000, 32000, -32000}
	ApplyPeakCeiling(over, 27000)
	if over[1] != 27000 {
		t.Fatalf("expected 27000, got %d", over[1])
	}
	if over[0] != 13500 {
		t.Fatalf("expected 13500, got %d", over[0])
	}
}

func TestEngine_PlayStreamConcurrency(t *testing.T) {
	e := &Engine{
		streams:        make(map[string]*audioStream),
		jitterBufferMS: 120,
		mixBuffer:      make([]int32, 4096),
		workItems:      make([]streamWorkItem, 0, 16),
		scratchBuffer:  make([]byte, 4096*2),
	}

	dummyFrame := EncodePCM(make([]int16, FrameSamples))

	// Run concurrent writers to multiple streams
	done := make(chan bool)
	for senderIdx := 0; senderIdx < 4; senderIdx++ {
		go func(id string) {
			for i := 0; i < 50; i++ {
				e.PlayStream(id, dummyFrame)
			}
			done <- true
		}(string(rune('A' + senderIdx)))
	}

	for i := 0; i < 4; i++ {
		<-done
	}

	e.playbackMu.Lock()
	if len(e.streams) != 4 {
		t.Fatalf("expected 4 streams, got %d", len(e.streams))
	}
	e.playbackMu.Unlock()
}
