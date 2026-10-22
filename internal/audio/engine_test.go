//go:build cgo

package audio

import (
	"encoding/binary"
	"math"
	"testing"
	"time"

	"voxmesh/internal/config"
)

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

func TestEngine_PlayStreamConcurrency(t *testing.T) {
	e := newEngineState()

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

var defaultOpus = func() OpusSettings {
	cfg := config.Default()
	return EncoderSettings(&cfg)
}()

// setPlayback configura el engine con los valores de fábrica, el tope de jitter maxMS y una
// cadena de salida opcional.
func setPlayback(e *Engine, maxMS int, filter *FilterSettings) {
	settings := defaultPlayback
	settings.Jitter.MaxMS = maxMS
	if filter != nil {
		settings.Filter, settings.FilterEnabled = *filter, true
	}
	e.playbackMu.Lock()
	e.jitter = settings.Jitter // los streams creados antes del primer mix ya usan este tope
	e.playbackMu.Unlock()
	e.SetPlaybackSettings(func() PlaybackSettings { return settings })
}

// encodeSine codifica count frames de una senoidal continua con Opus.
func encodeSine(t *testing.T, count int, amplitude float64) [][]byte {
	t.Helper()
	encoder, err := NewOpusEncoder(defaultOpus)
	if err != nil {
		t.Fatal(err)
	}
	packets := make([][]byte, 0, count)
	for n := 0; n < count; n++ {
		frame := sineFrame(n, amplitude)
		if err := encoder.Encode(frame, defaultOpus, func(packet []byte) {
			packets = append(packets, append([]byte(nil), packet...))
		}); err != nil {
			t.Fatal(err)
		}
	}
	return packets
}

func TestOpus_RoundTripIsSmallAndKeepsLevel(t *testing.T) {
	packets := encodeSine(t, 50, 12000)
	decoder, err := newOpusDecoder()
	if err != nil {
		t.Fatal(err)
	}
	pcm := make([]int16, FrameSamples)
	for n, packet := range packets {
		if len(packet) >= 200 {
			t.Fatalf("packet %d is %d bytes, expected < 200", n, len(packet))
		}
		got, err := decoder.Decode(packet, pcm)
		if err != nil || got != FrameSamples {
			t.Fatalf("decode packet %d: n=%d err=%v", n, got, err)
		}
	}
	// Después del arranque del codec, el nivel de la senoidal se mantiene (±3 dB).
	want := 12000 / math.Sqrt2
	if rms := frameRMS(pcm); rms < want/1.41 || rms > want*1.41 {
		t.Fatalf("decoded RMS %.0f, expected about %.0f", rms, want)
	}
}

func TestOpus_EncodeRejectsWrongFrameSize(t *testing.T) {
	encoder, err := NewOpusEncoder(defaultOpus)
	if err != nil {
		t.Fatal(err)
	}
	if err := encoder.Encode(make([]int16, 480), defaultOpus, func([]byte) {}); err == nil {
		t.Fatal("expected error for a 10 ms frame")
	}
}

// mixFrames corre el callback de playback count veces con buffers de 20 ms.
func mixFrames(e *Engine, count int) []int16 {
	out := make([]int16, 0, count*FrameSamples)
	buf := make([]byte, FrameSamples*2)
	for i := 0; i < count; i++ {
		e.mix(buf)
		for k := 0; k < FrameSamples; k++ {
			out = append(out, int16(binary.LittleEndian.Uint16(buf[k*2:])))
		}
	}
	return out
}

func TestEngine_PlaysOpusStreamWithVolume(t *testing.T) {
	packets := encodeSine(t, 20, 8000)

	full := newEngineState()
	setPlayback(full, 40, nil)
	half := newEngineState()
	setPlayback(half, 40, nil)
	// El volumen se fija antes de que exista el stream (como al reabrir el Engine).
	half.SetStreamVolume("peer", 0.5)
	for n, packet := range packets {
		full.PlayStreamSeq("peer", uint32(n), 0, packet)
		half.PlayStreamSeq("peer", uint32(n), 0, packet)
	}
	fullOut := mixFrames(full, 12)
	halfOut := mixFrames(half, 12)

	fullRMS := frameRMS(fullOut[5*FrameSamples:])
	halfRMS := frameRMS(halfOut[5*FrameSamples:])
	if fullRMS < 2000 {
		t.Fatalf("network stream not played: RMS %.0f", fullRMS)
	}
	if ratio := halfRMS / fullRMS; ratio < 0.45 || ratio > 0.55 {
		t.Fatalf("expected half volume, got ratio %.2f", ratio)
	}

	if ids := full.AudibleStreams(nil, time.Second); len(ids) != 1 || ids[0] != "peer" {
		t.Fatalf("expected peer audible, got %v", ids)
	}
	report := full.JitterReport()
	if stats, ok := report["peer"]; !ok || stats.Played < 10 {
		t.Fatalf("unexpected jitter report: %+v", report)
	}
}

func TestEngine_OutputFilterRunsPerStream(t *testing.T) {
	packets := encodeSine(t, 20, 8000)
	e := newEngineState()
	setPlayback(e, 40, &FilterSettings{GainDB: -6})
	plain := newEngineState()
	setPlayback(plain, 40, nil)
	for n, packet := range packets {
		e.PlayStreamSeq("peer", uint32(n), 0, packet)
		plain.PlayStreamSeq("peer", uint32(n), 0, packet)
	}
	ratio := frameRMS(mixFrames(e, 12)[5*FrameSamples:]) / frameRMS(mixFrames(plain, 12)[5*FrameSamples:])
	if ratio < 0.45 || ratio > 0.55 {
		t.Fatalf("expected -6 dB from the output chain, got ratio %.2f", ratio)
	}
}

func TestEngine_OpusFECRecoversLostPacket(t *testing.T) {
	packets := encodeSine(t, 30, 8000)
	e := newEngineState()
	setPlayback(e, 60, nil)
	// Llegan a ritmo real: 3 adelantados y después uno por cada buffer reproducido.
	push := func(n int) {
		if n < len(packets) && n != 12 { // el 12 se pierde: lo tiene que rearmar el FEC del 13
			e.PlayStreamSeq("peer", uint32(n), 0, packets[n])
		}
	}
	for n := 0; n < 3; n++ {
		push(n)
	}
	out := make([]int16, 0, 25*FrameSamples)
	for k := 0; k < 25; k++ {
		push(k + 3)
		out = append(out, mixFrames(e, 1)...)
	}
	stats := e.JitterReport()["peer"]
	if stats.Recovered != 1 || stats.Concealed != 0 || stats.Dropped != 0 {
		t.Fatalf("expected the lost packet to be recovered with FEC: %+v", stats)
	}
	// Sin agujero: ningún frame reproducido después del arranque queda en silencio.
	for f := 2; f < 25; f++ {
		if rms := frameRMS(out[f*FrameSamples : (f+1)*FrameSamples]); rms < 1000 {
			t.Fatalf("frame %d dropped to RMS %.0f", f, rms)
		}
	}
}
