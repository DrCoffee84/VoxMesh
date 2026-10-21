//go:build cgo

package audio

import (
	"fmt"
	"sync"

	opus "gopkg.in/hraban/opus.v2"

	"voxmesh/internal/config"
)

// El audio de red es siempre Opus: 48 kHz mono, un paquete por frame de 20 ms. Necesita libopus
// (pkg-config) y el build tag nolibopusfile (ver AGENT.md §4).

// newOpusDecoder crea el decoder de un emisor. *opus.Decoder ya cumple frameDecoder.
func newOpusDecoder() (frameDecoder, error) {
	return opus.NewDecoder(SampleRate, Channels)
}

// OpusEncoder codifica el audio saliente. Uno por App: se recrea al conectar.
type OpusEncoder struct {
	mu  sync.Mutex
	enc *opus.Encoder
	out []byte
}

func NewOpusEncoder() (*OpusEncoder, error) {
	enc, err := opus.NewEncoder(SampleRate, Channels, opus.AppVoIP)
	if err != nil {
		return nil, fmt.Errorf("crear encoder Opus: %w", err)
	}
	// DTX de Opus apagado: la compuerta/VAD propia ya hace DTX (no se manda nada en silencio).
	// FEC in-band: cada paquete lleva una versión de baja tasa del anterior, que el receptor usa
	// si ese se pierde (DecodeFEC).
	for _, step := range []struct {
		name string
		err  error
	}{
		{"bitrate", enc.SetBitrate(config.OpusBitrate)},
		{"complexity", enc.SetComplexity(config.OpusComplexity)},
		{"FEC", enc.SetInBandFEC(true)},
		{"packet loss", enc.SetPacketLossPerc(config.OpusPacketLossPerc)},
		{"DTX", enc.SetDTX(false)},
	} {
		if step.err != nil {
			return nil, fmt.Errorf("configurar encoder Opus (%s): %w", step.name, step.err)
		}
	}
	return &OpusEncoder{enc: enc, out: make([]byte, config.OpusMaxPacketBytes)}, nil
}

// Encode codifica un frame de exactamente FrameSamples muestras y llama a send con el paquete.
// El paquete es un buffer reutilizado: solo vale durante send (el lock evita que dos emisores,
// micrófono y celular, se pisen el buffer o el estado del encoder).
func (e *OpusEncoder) Encode(samples []int16, send func(packet []byte)) error {
	if len(samples) != FrameSamples {
		return fmt.Errorf("frame de %d muestras, se esperaban %d", len(samples), FrameSamples)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	n, err := e.enc.Encode(samples, e.out)
	if err != nil {
		return fmt.Errorf("codificar Opus: %w", err)
	}
	send(e.out[:n])
	return nil
}
