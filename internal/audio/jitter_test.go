package audio

import (
	"math"
	"testing"
	"time"

	"voxmesh/internal/config"
)

var jitterT0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// fakeDecoder simula el decoder Opus sin CGO. Un paquete es [fec?][PCM S16LE][PCM del frame
// anterior si fec=1]. El PLC continúa el último frame en espejo (sin salto en el empalme, como
// el PLC real) con una ganancia que baja de 1 a 0.5.
type fakeDecoder struct {
	last []int16
}

func fakePacket(frame []int16, fec []int16) []byte {
	packet := []byte{0}
	packet = append(packet, EncodePCM(frame)...)
	if fec != nil {
		packet[0] = 1
		packet = append(packet, EncodePCM(fec)...)
	}
	return packet
}

func (d *fakeDecoder) Decode(packet []byte, pcm []int16) (int, error) {
	primary, _ := DecodePCM(packet[1 : 1+FrameSamples*2])
	n := copy(pcm, primary)
	d.last = append(d.last[:0], pcm[:n]...)
	return n, nil
}

func (d *fakeDecoder) DecodeFEC(packet []byte, pcm []int16) error {
	if packet[0] != 1 {
		return d.DecodePLC(pcm)
	}
	fec, _ := DecodePCM(packet[1+FrameSamples*2:])
	copy(pcm, fec)
	d.last = append(d.last[:0], pcm...)
	return nil
}

func (d *fakeDecoder) DecodePLC(pcm []int16) error {
	if len(d.last) == 0 {
		clear(pcm)
		return nil
	}
	n := len(d.last)
	for i := range pcm {
		pcm[i] = int16(float64(d.last[n-1-i%n]) * (1 - 0.5*float64(i)/float64(len(pcm))))
	}
	d.last = append(d.last[:0], pcm...)
	return nil
}

func newTestJitter(targetMS int) *jitterBuffer {
	return newJitterBuffer(targetMS, &fakeDecoder{})
}

// push manda un frame sin FEC en la frase 0.
func push(j *jitterBuffer, seq uint32, frame []int16, now time.Time) {
	j.Push(seq, 0, fakePacket(frame, nil), now)
}

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
	buf := make([]int16, FrameSamples)
	for f := 0; f < count; f++ {
		j.Read(buf, start.Add(time.Duration(f)*20*time.Millisecond))
		out = append(out, buf...)
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
	j := newTestJitter(200) // 10 frames de colchón
	var want []int16
	for n := 0; n < 10; n++ {
		frame := rampFrame(n)
		want = append(want, frame...)
		push(j, uint32(100+n), frame, jitterT0)
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
	j := newTestJitter(200)
	order := []int{0, 2, 1, 3, 5, 4, 6, 9, 8, 7}
	var want []int16
	for n := 0; n < 10; n++ {
		want = append(want, rampFrame(n)...)
	}
	for _, n := range order {
		push(j, uint32(n), rampFrame(n), jitterT0)
	}
	out := readFrames(j, jitterT0, 9)
	for i := range out {
		if out[i] != want[i] {
			t.Fatalf("sample %d: got %d, want %d (reorder failed)", i, out[i], want[i])
		}
	}
}

func TestJitterBuffer_LostPacketIsConcealedWithoutAdvancingAudio(t *testing.T) {
	j := newTestJitter(200)
	for n := 0; n < 10; n++ {
		if n == 4 {
			continue // perdido
		}
		push(j, uint32(n), sineFrame(n, 20000), jitterT0)
	}
	out := readFrames(j, jitterT0, 9)

	// Sin FEC en el paquete 5, el hueco se rellena con PLC atenuado, no con silencio ni
	// adelantando el frame 5. El PLC sale continuo después del frame 3 (sin crossfade); su
	// ganancia baja de 1.0 a 0.5 a lo largo del frame: medimos el último tramo.
	f3 := sineFrame(3, 20000)
	idx3 := indexOf(out, f3)
	if idx3 < 0 {
		t.Fatal("frame 3 not played intact")
	}
	plcStart := idx3 + FrameSamples
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
	if s := j.Stats(); s.Lost() != 1 {
		t.Fatalf("expected 1 lost frame, got %+v", s)
	}
}

func TestJitterBuffer_LatePacketIsDropped(t *testing.T) {
	j := newTestJitter(200)
	for n := 0; n < 10; n++ {
		if n != 4 {
			push(j, uint32(n), sineFrame(n, 20000), jitterT0)
		}
	}
	readFrames(j, jitterT0, 6)
	push(j, 4, sineFrame(4, 20000), jitterT0.Add(120*time.Millisecond))
	if s := j.Stats(); s.Late != 1 {
		t.Fatalf("expected late packet to be dropped, got %+v", s)
	}
}

func TestJitterBuffer_UnderrunWaitsForDelayedPacket(t *testing.T) {
	j := newTestJitter(40) // 2 frames de colchón
	for n := 0; n < 3; n++ {
		push(j, uint32(n), rampFrame(n), jitterT0)
	}
	// Se reproducen los 3 frames y el buffer se vacía (pico de lag de ~60 ms).
	readFrames(j, jitterT0, 6)
	// Llega el frame 3 demorado: no se descarta y suena apenas llega.
	resumeAt := jitterT0.Add(120 * time.Millisecond)
	push(j, 3, rampFrame(3), resumeAt)
	out := readFrames(j, resumeAt, 2)
	f3 := rampFrame(3)
	if indexOf(out, f3[config.JitterSpliceSamples:FrameSamples-config.JitterSpliceSamples]) < 0 {
		t.Fatal("delayed frame 3 was not played after the underrun")
	}
	if s := j.Stats(); s.Late != 0 || s.Lost() != 0 {
		t.Fatalf("delayed frame must not count as lost or late: %+v", s)
	}
}

func TestJitterBuffer_FadesToSilenceWhenStreamStops(t *testing.T) {
	j := newTestJitter(40)
	for n := 0; n < 3; n++ {
		push(j, uint32(n), sineFrame(n, 20000), jitterT0)
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
	j := newTestJitter(40)
	for n := 0; n < 3; n++ {
		push(j, uint32(50000+n), rampFrame(n), jitterT0)
	}
	readFrames(j, jitterT0, 4)
	later := jitterT0.Add(5 * time.Second)
	for n := 0; n < 3; n++ {
		push(j, uint32(1+n), rampFrame(n), later)
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
	j := newTestJitter(40) // objetivo 2 frames
	for n := 0; n < 20; n++ {
		push(j, uint32(n), sineFrame(n, 20000), jitterT0) // ráfaga de 400 ms
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

func TestJitterBuffer_LostPacketIsRecoveredWithFEC(t *testing.T) {
	j := newTestJitter(200)
	for n := 0; n < 10; n++ {
		if n == 4 {
			continue // perdido
		}
		var fec []int16
		if n == 5 {
			fec = rampFrame(4) // el paquete 5 trae la redundancia del 4
		}
		j.Push(uint32(n), 0, fakePacket(rampFrame(n), fec), jitterT0)
	}
	out := readFrames(j, jitterT0, 9)
	// El FEC sale del decoder continuo con lo anterior: el frame 4 suena entero, sin crossfade.
	var want []int16
	for n := 0; n < 8; n++ {
		want = append(want, rampFrame(n)...)
	}
	for i := range want[:len(out)-FrameSamples] {
		if out[i] != want[i] {
			t.Fatalf("sample %d: got %d, want %d", i, out[i], want[i])
		}
	}
	if s := j.Stats(); s.Recovered != 1 || s.Concealed != 0 || s.Played != 9 {
		t.Fatalf("unexpected stats: %+v", s)
	}
}

func TestJitterBuffer_DriftWaitsForQuietFrame(t *testing.T) {
	j := newTestJitter(40) // objetivo 2 frames: hay deriva con más de 4 en el buffer
	quiet := func(n int) bool { return n == 3 || n == 13 }
	frame := func(n int) []int16 {
		if quiet(n) {
			f := make([]int16, FrameSamples)
			for i := range f {
				f[i] = 50
			}
			return f
		}
		return rampFrame(n)
	}
	// Colchón de 8 frames (más que objetivo + slack, menos que el umbral de descarte forzado)
	// que se mantiene: llega un frame por cada uno que se reproduce.
	for n := 0; n < 7; n++ {
		push(j, uint32(n), frame(n), jitterT0)
	}
	var out []int16
	buf := make([]int16, FrameSamples)
	for k := 0; k < 14; k++ {
		now := jitterT0.Add(time.Duration(k) * 20 * time.Millisecond)
		push(j, uint32(7+k), frame(7+k), now)
		j.Read(buf, now)
		out = append(out, buf...)
	}
	s := j.Stats()
	if s.Dropped != 2 {
		t.Fatalf("expected the 2 quiet frames to be dropped, got %+v", s)
	}
	// Ningún frame con voz se descartó: todos aparecen (el interior, sin la zona de crossfade).
	for n := 0; n < 14; n++ {
		if quiet(n) {
			continue
		}
		f := rampFrame(n)
		if indexOf(out, f[config.JitterSpliceSamples:FrameSamples-config.JitterSpliceSamples]) < 0 {
			t.Fatalf("loud frame %d was dropped", n)
		}
	}
}

func TestJitterBuffer_AdaptiveTargetFollowsMeasuredJitter(t *testing.T) {
	steady := newTestJitter(200)
	for n := 0; n < 100; n++ {
		push(steady, uint32(n), rampFrame(0), jitterT0.Add(time.Duration(n)*20*time.Millisecond))
	}
	if got := steady.adaptiveTarget(); got != 2 {
		t.Fatalf("steady network should use the minimum target, got %d frames", got)
	}
	if s := steady.Stats(); s.JitterMS > 1 || s.DelayP95MS > 5 {
		t.Fatalf("steady network measured jitter: %+v", s)
	}

	// Uno de cada 4 paquetes llega 70 ms tarde: el colchón tiene que cubrir esos 70 ms.
	jittery := newTestJitter(200)
	for n := 0; n < 100; n++ {
		at := jitterT0.Add(time.Duration(n) * 20 * time.Millisecond)
		if n%4 == 0 {
			at = at.Add(70 * time.Millisecond)
		}
		push(jittery, uint32(n), rampFrame(0), at)
	}
	got := jittery.adaptiveTarget()
	if got < 5 || got > 6 {
		t.Fatalf("expected target to cover ~75 ms (+1 margin), got %d frames", got)
	}

	// Sin datos suficientes se usa el tope.
	fresh := newTestJitter(200)
	push(fresh, 0, rampFrame(0), jitterT0)
	if got := fresh.adaptiveTarget(); got != 10 {
		t.Fatalf("expected max target without data, got %d", got)
	}
}

func TestJitterBuffer_TalkspurtGapIsNotMeasuredAsDelay(t *testing.T) {
	j := newTestJitter(200)
	// Dos frases de 60 frames con secuencia contigua y 2 s de silencio en el medio (DTX).
	for n := 0; n < 120; n++ {
		at := jitterT0.Add(time.Duration(n) * 20 * time.Millisecond)
		spurt := uint8(0)
		if n >= 60 {
			at = at.Add(2 * time.Second)
			spurt = 1
		}
		j.Push(uint32(n), spurt, fakePacket(rampFrame(0), nil), at)
	}
	if s := j.Stats(); s.DelayP95MS > 5 {
		t.Fatalf("DTX silence measured as delay: %+v", s)
	}
}

func TestJitterBuffer_TuningBoundsAdaptiveTarget(t *testing.T) {
	tuning := DefaultJitterTuning()
	tuning.MinMS, tuning.MaxMS = 100, 300
	j := newJitterBufferTuned(tuning, &fakeDecoder{})
	if j.adaptiveTarget() != 15 {
		t.Fatalf("without measurements the target is the max: %d", j.adaptiveTarget())
	}
	for i := 0; i < 60; i++ {
		j.arrival.record(0) // red perfecta
	}
	if got := j.adaptiveTarget(); got != 5 {
		t.Fatalf("perfect network should sit on the 100 ms floor (5 frames), got %d", got)
	}
	tuning.MinMS = 40
	j.setTuning(tuning)
	if got := j.adaptiveTarget(); got != 2 {
		t.Fatalf("lower floor: want 2 frames (margin 1 + bucket), got %d", got)
	}
	tuning.MaxMS = 40
	j.setTuning(tuning)
	if j.maxFrames != 2 || j.targetFrames > 2 {
		t.Fatalf("max not applied: max %d target %d", j.maxFrames, j.targetFrames)
	}
}
