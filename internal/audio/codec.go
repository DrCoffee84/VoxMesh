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

// OpusEncoder codifica el audio saliente. Uno por App: se recrea al conectar. La configuración
// (bitrate, complejidad, FEC...) se aplica en vivo: Encode la compara con la vigente.
type OpusEncoder struct {
	mu       sync.Mutex
	enc      *opus.Encoder
	settings OpusSettings
	out      []byte
}

func NewOpusEncoder(settings OpusSettings) (*OpusEncoder, error) {
	e := &OpusEncoder{out: make([]byte, config.OpusMaxPacketBytes)}
	if err := e.configure(settings); err != nil {
		return nil, err
	}
	return e, nil
}

// configure aplica settings; cambiar entre voz y música crea un encoder nuevo (Opus no cambia
// de aplicación en caliente). Se llama con mu tomado (o antes de publicar el encoder).
func (e *OpusEncoder) configure(settings OpusSettings) error {
	if e.enc == nil || settings.Music != e.settings.Music {
		application := opus.AppVoIP
		if settings.Music {
			application = opus.AppAudio
		}
		enc, err := opus.NewEncoder(SampleRate, Channels, application)
		if err != nil {
			return fmt.Errorf("crear encoder Opus: %w", err)
		}
		e.enc = enc
	}
	// DTX de Opus apagado: la compuerta/VAD propia ya hace DTX (no se manda nada en silencio).
	// FEC in-band: cada paquete lleva una versión de baja tasa del anterior, que el receptor usa
	// si ese se pierde (DecodeFEC).
	for _, step := range []struct {
		name string
		err  error
	}{
		{"bitrate", e.enc.SetBitrate(settings.BitrateKbps * 1000)},
		{"complexity", e.enc.SetComplexity(settings.Complexity)},
		{"FEC", e.enc.SetInBandFEC(settings.FEC)},
		{"packet loss", e.enc.SetPacketLossPerc(settings.LossPerc)},
		{"DTX", e.enc.SetDTX(false)},
	} {
		if step.err != nil {
			return fmt.Errorf("configurar encoder Opus (%s): %w", step.name, step.err)
		}
	}
	e.settings = settings
	return nil
}

// Encode codifica un frame de exactamente FrameSamples muestras con settings y llama a send con
// el paquete. El paquete es un buffer reutilizado: solo vale durante send (el lock evita que dos
// emisores, micrófono y celular, se pisen el buffer o el estado del encoder).
func (e *OpusEncoder) Encode(samples []int16, settings OpusSettings, send func(packet []byte)) error {
	if len(samples) != FrameSamples {
		return fmt.Errorf("frame de %d muestras, se esperaban %d", len(samples), FrameSamples)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if settings != e.settings {
		if err := e.configure(settings); err != nil {
			return err
		}
	}
	n, err := e.enc.Encode(samples, e.out)
	if err != nil {
		return fmt.Errorf("codificar Opus: %w", err)
	}
	send(e.out[:n])
	return nil
}
