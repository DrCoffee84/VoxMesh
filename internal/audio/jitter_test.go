package audio

import (
	"math"
	"testing"
	"time"

	"voxmesh/internal/config"
)

var jitterT0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// sineFrame devuelve el frame n de una senoidal continua (los frames empalman sin salto).
// 437 Hz no repite su forma de onda dentro de un segundo a 48 kHz, así cada frame es único.
func sineFrame(n int, amplitude float64) []int16 {
	frame := make([]int16, FrameSamples)
	for i := range frame {
		t := float64(n*FrameSamples+i) / SampleRate
		frame[i] = int16(amplitude * math.Sin(2*math.Pi*437*t))
	}
	return frame
}

// rampFrame devuelve un frame con valores únicos para comparar bit a bit.
func rampFrame(n int) []int16 {
	frame := make([]int16, FrameSamples)
	for i := range frame {
		frame[i] = int16(n*FrameSamples + i - 16000)
	}
	return frame
}

// readFrames lee count frames de 20 ms avanzando el reloj simulado.
func readFrames(j *jitterBuffer, start time.Time, count int) []int16 {
	out := make([]int16, 0, count*FrameSamples)
	buf := make([]byte, FrameSamples*2)
	for f := 0; f < count; f++ {
		j.Read(buf, start.Add(time.Duration(f)*20*time.Millisecond))
		decoded, _ := DecodePCM(buf)
		out = append(out, decoded...)
	}
	return out
}

func indexOf(haystack, needle []int16) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for k := range needle {
			if haystack[i+k] != needle[k] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

func peakOf(samples []int16) int {
	peak := 0
	for _, s := range samples {
		v := int(s)
		if v < 0 {
			v = -v
		}
		if v > peak {
			peak = v
		}
	}
	return peak
}

func TestJitterBuffer_InOrderIsBitExact(t *testing.T) {
	j := newJitterBuffer(200) // 10 frames de colchón
	var want []int16
	for n := 0; n < 10; n++ {
		frame := rampFrame(n)
		want = append(want, frame...)
		j.Push(uint32(100+n), EncodePCM(frame), jitterT0)
	}
	out := readFrames(j, jitterT0, 9)
	for i := range out {
		if out[i] != want[i] {
			t.Fatalf("sample %d: got %d, want %d", i, out[i], want[i])
		}
	}
	if s := j.Stats(); s.Played != 10 || s.Concealed != 0 || s.Dropped != 0 {
		t.Fatalf("unexpected stats: %+v", s)
	}
}

func TestJitterBuffer_ReordersOutOfOrderPackets(t *testing.T) {
	j := newJitterBuffer(200)
	order := []int{0, 2, 1, 3, 5, 4, 6, 9, 8, 7}
	var want []int16
	for n := 0; n < 10; n++ {
		want = append(want, rampFrame(n)...)
	}
	for _, n := range order {
		j.Push(uint32(n), EncodePCM(rampFrame(n)), jitterT0)
	}
	out := readFrames(j, jitterT0, 9)
	for i := range out {
		if out[i] != want[i] {
			t.Fatalf("sample %d: got %d, want %d (reorder failed)", i, out[i], want[i])
		}
	}
}

func TestJitterBuffer_LostPacketIsConcealedWithoutAdvancingAudio(t *testing.T) {
	j := newJitterBuffer(200)
	for n := 0; n < 10; n++ {
		if n == 4 {
			continue // perdido
		}
		j.Push(uint32(n), EncodePCM(sineFrame(n, 20000)), jitterT0)
	}
	out := readFrames(j, jitterT0, 9)

	// El hueco se rellena con PLC atenuado, no con silencio ni adelantando el frame 5.
	// La salida va retrasada JitterSpliceSamples (cola retenida), así que el frame
	// ocultado arranca en 4*FrameSamples-JitterSpliceSamples. Su ganancia baja de 1.0
	// a JitterPLCAttenuation a lo largo del frame: medimos el último tramo.
	plcStart := 4*FrameSamples - config.JitterSpliceSamples
	tailPeak := peakOf(out[plcStart+600 : plcStart+800])
	if tailPeak == 0 || tailPeak > int(20000*0.7) {
		t.Fatalf("expected attenuated concealment, got peak %d", tailPeak)
	}
	// El frame 5 sigue apareciendo (empalmado) y el 6 sale intacto a continuación.
	f5 := sineFrame(5, 20000)
	idx5 := indexOf(out, f5[config.JitterSpliceSamples:FrameSamples-config.JitterSpliceSamples])
	if idx5 < 4*FrameSamples {
		t.Fatalf("frame 5 not found after concealment (index %d)", idx5)
	}
	if idx6 := indexOf(out, sineFrame(6, 20000)); idx6 < 0 {
		t.Fatal("frame 6 not played intact after recovery")
	}
	if s := j.Stats(); s.Concealed != 1 {
		t.Fatalf("expected 1 concealed frame, got %+v", s)
	}
}

func TestJitterBuffer_LatePacketIsDropped(t *testing.T) {
	j := newJitterBuffer(200)
	for n := 0; n < 10; n++ {
		if n != 4 {
			j.Push(uint32(n), EncodePCM(sineFrame(n, 20000)), jitterT0)
		}
	}
	readFrames(j, jitterT0, 6)
	j.Push(4, EncodePCM(sineFrame(4, 20000)), jitterT0.Add(120*time.Millisecond))
	if s := j.Stats(); s.Late != 1 {
		t.Fatalf("expected late packet to be dropped, got %+v", s)
	}
}

func TestJitterBuffer_UnderrunWaitsForDelayedPacket(t *testing.T) {
	j := newJitterBuffer(40) // 2 frames de colchón
	for n := 0; n < 3; n++ {
		j.Push(uint32(n), EncodePCM(rampFrame(n)), jitterT0)
	}
	// Se reproducen los 3 frames y el buffer se vacía (pico de lag de ~60 ms).
	readFrames(j, jitterT0, 6)
	// Llega el frame 3 demorado: no se descarta y suena apenas llega.
	resumeAt := jitterT0.Add(120 * time.Millisecond)
	j.Push(3, EncodePCM(rampFrame(3)), resumeAt)
	out := readFrames(j, resumeAt, 2)
	f3 := rampFrame(3)
	if indexOf(out, f3[config.JitterSpliceSamples:FrameSamples-config.JitterSpliceSamples]) < 0 {
		t.Fatal("delayed frame 3 was not played after the underrun")
	}
	if s := j.Stats(); s.Late != 0 || s.Concealed != 0 {
		t.Fatalf("delayed frame must not count as lost or late: %+v", s)
	}
}

func TestJitterBuffer_FadesToSilenceWhenStreamStops(t *testing.T) {
	j := newJitterBuffer(40)
	for n := 0; n < 3; n++ {
		j.Push(uint32(n), EncodePCM(sineFrame(n, 20000)), jitterT0)
	}
	out := readFrames(j, jitterT0, 6)
	// Después de los 3 frames y el fade (~10 ms), todo silencio: sin clic ni ruido colgado.
	silentFrom := 3*FrameSamples + config.JitterFadeOutSamples
	for i := silentFrom; i < len(out); i++ {
		if out[i] != 0 {
			t.Fatalf("expected silence after fade, sample %d = %d", i, out[i])
		}
	}
	// El fade arranca cerca del nivel de la voz y baja de forma continua.
	fade := out[3*FrameSamples-config.JitterSpliceSamples : silentFrom]
	for i := 1; i < len(fade); i++ {
		if d := int(fade[i]) - int(fade[i-1]); d > 2000 || d < -2000 {
			t.Fatalf("discontinuity in fade at %d: %d -> %d", i, fade[i-1], fade[i])
		}
	}
}

func TestJitterBuffer_SenderRestartResets(t *testing.T) {
	j := newJitterBuffer(40)
	for n := 0; n < 3; n++ {
		j.Push(uint32(50000+n), EncodePCM(rampFrame(n)), jitterT0)
	}
	readFrames(j, jitterT0, 4)
	later := jitterT0.Add(5 * time.Second)
	for n := 0; n < 3; n++ {
		j.Push(uint32(1+n), EncodePCM(rampFrame(n)), later)
	}
	out := readFrames(j, later, 4)
	f1 := rampFrame(1)
	if indexOf(out, f1) < 0 {
		t.Fatal("audio after sender restart was not played")
	}
	if s := j.Stats(); s.Resets != 1 {
		t.Fatalf("expected 1 reset, got %+v", s)
	}
}

func TestJitterBuffer_DriftDropsFramesGradually(t *testing.T) {
	j := newJitterBuffer(40) // objetivo 2 frames
	for n := 0; n < 20; n++ {
		j.Push(uint32(n), EncodePCM(sineFrame(n, 20000)), jitterT0) // ráfaga de 400 ms
	}
	out := readFrames(j, jitterT0, 20)
	s := j.Stats()
	if s.Dropped == 0 {
		t.Fatalf("expected drift correction to drop frames, got %+v", s)
	}
	if s.Dropped > 20/config.JitterDriftCooldownFrames+1 {
		t.Fatalf("drift correction too aggressive: %+v", s)
	}
	// Los empalmes por descarte no deben generar saltos bruscos.
	for i := 1; i < len(out); i++ {
		if d := int(out[i]) - int(out[i-1]); d > 3000 || d < -3000 {
			t.Fatalf("discontinuity at sample %d: %d -> %d", i, out[i-1], out[i])
		}
	}
}
