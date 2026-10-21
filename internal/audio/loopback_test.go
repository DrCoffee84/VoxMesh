//go:build cgo

package audio

import (
	"math"
	"math/rand"
	"sync"
	"testing"
	"time"

	"voxmesh/internal/transport"
)

// TestLoopbackRoom simula una sala en tiempo real por UDP de verdad (127.0.0.1): un emisor que
// codifica en Opus y manda a ritmo de 20 ms con frases y silencios (talkspurt), una "red" con
// pérdida y jitter, y un receptor con el jitter buffer y el mezclador corriendo como el callback
// de malgo. Mide los criterios de la agenda que no dependen de oídos humanos: tamaño de paquete,
// pérdida y jitter medidos, recuperación FEC, ausencia de huecos y colchón adaptativo.
func TestLoopbackRoom(t *testing.T) {
	if testing.Short() {
		t.Skip("prueba de tiempo real")
	}
	receiver, err := transport.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Close()
	sender, err := transport.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()

	engine := newEngineState()
	engine.SetJitterBufferMS(200)
	encoder, err := NewOpusEncoder()
	if err != nil {
		t.Fatal(err)
	}

	// Receptor: igual que handleAudioPacket, el paquete va directo al jitter buffer.
	var maxPacket int
	var mu sync.Mutex
	go func() {
		for {
			packet, _, err := receiver.Receive()
			if err != nil {
				return
			}
			mu.Lock()
			if size := transport.HeaderSize + len(packet.Payload); size > maxPacket {
				maxPacket = size
			}
			mu.Unlock()
			engine.PlayStreamSeq("peer", packet.Sequence, packet.Talkspurt, packet.Payload)
		}
	}()

	// "Callback de malgo": un buffer de 20 ms cada 20 ms; guarda el RMS de cada uno.
	stop := make(chan struct{})
	var levels []float64
	var levelsMu sync.Mutex
	go func() {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		buf := make([]byte, FrameSamples*2)
		pcm := make([]int16, FrameSamples)
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				engine.mix(buf)
				for i := range pcm {
					pcm[i] = int16(uint16(buf[2*i]) | uint16(buf[2*i+1])<<8)
				}
				levelsMu.Lock()
				levels = append(levels, frameRMS(pcm))
				levelsMu.Unlock()
			}
		}
	}()

	// Emisor: 3 frases de 1.6 s separadas por 600 ms de silencio (VAD cerrado: no se manda nada,
	// la secuencia sigue contigua y el talkspurt cambia). Red: 3 % de pérdida y 0–25 ms de jitter.
	rng := rand.New(rand.NewSource(1))
	const phrases, phraseFrames = 3, 80
	var seq uint32
	sent, lost := 0, 0
	start := time.Now()
	for phrase := 0; phrase < phrases; phrase++ {
		for f := 0; f < phraseFrames; f++ {
			n := phrase*phraseFrames + f
			frame := make([]int16, FrameSamples)
			for i := range frame {
				tt := float64(n*FrameSamples+i) / SampleRate
				frame[i] = int16(9000 * math.Sin(2*math.Pi*220*tt) * (0.7 + 0.3*math.Sin(2*math.Pi*3*tt)))
			}
			seq++
			sent++
			drop := rng.Float64() < 0.03
			delay := time.Duration(rng.Intn(25)) * time.Millisecond
			packetSeq, talkspurt := seq, uint8(phrase)
			if err := encoder.Encode(frame, func(opusPacket []byte) {
				if drop {
					lost++
					return
				}
				payload := append([]byte(nil), opusPacket...)
				time.AfterFunc(delay, func() {
					_ = sender.Send(receiver.Address(), transport.Packet{
						Kind: transport.PacketAudio, Flags: transport.FlagAudioSequence,
						Sequence: packetSeq, Talkspurt: talkspurt, Payload: payload,
					})
				})
			}); err != nil {
				t.Fatal(err)
			}
			// Ritmo real del micrófono, sin acumular error del sleep.
			time.Sleep(time.Until(start.Add(time.Duration(sent) * 20 * time.Millisecond)))
		}
		start = start.Add(600 * time.Millisecond)
		time.Sleep(600 * time.Millisecond)
	}
	time.Sleep(400 * time.Millisecond)
	close(stop)

	stats := engine.JitterReport()["peer"]
	mu.Lock()
	packetSize := maxPacket
	mu.Unlock()
	t.Logf("enviados=%d perdidos en la red=%d | paquete máx=%d bytes", sent, lost, packetSize)
	t.Logf("Audio de peer: reproducidos=%d recuperados=%d ocultados=%d tardíos=%d descartados=%d | buffer=%d frames objetivo=%d ms jitter=%.1f ms p95=%d ms",
		stats.Played, stats.Recovered, stats.Concealed, stats.Late, stats.Dropped, stats.BufferedFrames, stats.TargetMS, stats.JitterMS, stats.DelayP95MS)

	if packetSize >= 200 {
		t.Fatalf("paquete de %d bytes, la agenda pide < 200", packetSize)
	}
	if stats.Lost() < uint64(lost) || stats.Lost() > uint64(lost)+uint64(stats.Late)+3 {
		t.Fatalf("la pérdida medida (%d) no coincide con la de la red (%d)", stats.Lost(), lost)
	}
	if stats.DelayP95MS < 0 || stats.DelayP95MS > 60 {
		t.Fatalf("p95 de variación de retardo fuera de rango para 0–25 ms de jitter: %d ms", stats.DelayP95MS)
	}
	if stats.TargetMS >= 200 {
		t.Fatalf("el colchón no se adaptó: sigue en el máximo (%d ms)", stats.TargetMS)
	}

	// Sin huecos dentro de cada frase: contamos buffers en silencio entre el primer y el último
	// buffer con audio de cada tramo sonoro.
	levelsMu.Lock()
	defer levelsMu.Unlock()
	silentInside, voiced := 0, 0
	inPhrase := false
	run := 0
	for _, level := range levels {
		if level > 500 {
			if inPhrase && run > 0 && run < 10 { // hueco corto dentro de una frase
				silentInside += run
			}
			inPhrase = true
			run = 0
			voiced++
		} else if inPhrase {
			run++
		}
	}
	t.Logf("buffers con voz=%d, buffers en silencio dentro de frases=%d", voiced, silentInside)
	if silentInside > 0 {
		t.Fatalf("hubo %d buffers de silencio en medio de una frase (cortes)", silentInside)
	}
	if voiced < phrases*phraseFrames*9/10 {
		t.Fatalf("se reprodujeron %d buffers con voz de %d enviados", voiced, phrases*phraseFrames)
	}
}
