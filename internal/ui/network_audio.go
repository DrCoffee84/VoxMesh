//go:build cgo

package ui

import (
	"encoding/hex"
	"net"
	"time"

	"voxmesh/internal/audio"
	"voxmesh/internal/config"
	"voxmesh/internal/logging"
	"voxmesh/internal/transport"
)

// Payload de PacketAudio: [idLen][ID del emisor en bytes crudos][paquete Opus]. El ID de
// participante es el hex de 8 bytes (room.ParticipantID): viaja crudo y el receptor lo vuelve
// a pasar a hex para compararlo con los de la sala.
const (
	participantIDBytes = 8
	maxOpusPacketBytes = 1275
)

// talkspurtGap: si pasó más que esto desde el último paquete de audio enviado (compuerta
// cerrada, mute, corte del celular), el próximo arranca una frase nueva (Packet.Talkspurt).
const talkspurtGap = 60 * time.Millisecond

// handleAudioPacket es el único punto de entrada del audio recibido por red: separa el ID
// del emisor del paquete Opus y lo entrega al jitter buffer del Engine, que reordena,
// descarta los tardíos, decodifica y oculta las pérdidas. Lo usan las dos ramas de
// receiveLoop (host y cliente); el reenvío a otros peers lo hace receiveLoop en modo host.
func (a *App) handleAudioPacket(packet transport.Packet) {
	payload := packet.Payload
	if packet.Flags&transport.FlagAudioSequence == 0 || len(payload) < 2 {
		return
	}
	idLen := int(payload[0])
	if idLen == 0 || len(payload) <= 1+idLen {
		return
	}
	opusPacket := payload[1+idLen:]
	if len(opusPacket) > maxOpusPacketBytes {
		return
	}
	if engine := a.engine(); engine != nil {
		engine.PlayStreamSeq(hex.EncodeToString(payload[1:1+idLen]), packet.Sequence, packet.Talkspurt, opusPacket)
	}
}

// sendOutgoingPCM corre la cadena de envío sobre un frame capturado y manda lo que corresponda
// (nada con la compuerta cerrada; al abrirse, el pre-roll y el frame actual), codificado en Opus.
func (a *App) sendOutgoingPCM(frame []byte, udp *transport.UDP, peer *net.UDPAddr, host bool) {
	frames, ok := a.prepareOutgoingFrames(frame)
	// Mute: la cadena sigue corriendo (RNNoise retiene 20 ms y el VAD su hold; saltearla dejaría
	// audio viejo que saldría al desmutear) pero el frame se descarta igual que con la compuerta
	// cerrada (DTX): sin paquete, sin monitoreo ni indicador de voz. audioSequence no avanza, así
	// que el jitter buffer del receptor lo ve como silencio, no como pérdida.
	if !ok || len(frames) == 0 || a.isMuted.Load() {
		return
	}
	if engine := a.engine(); engine != nil && a.conf().LiveMonitoring {
		for _, samples := range frames {
			engine.Play(audio.EncodePCM(samples))
		}
	}
	a.flashVoiceIndicator()

	if !host && (peer == nil || peer.IP == nil || peer.IP.IsUnspecified()) {
		// Dirección ya resuelta al publicarse (setEndpoint): sin ResolveUDPAddr por frame.
		if peer = a.endpointAddr(); peer == nil {
			logging.Errorf("enviar audio saliente: host no disponible")
			return
		}
	}
	var senderID [participantIDBytes]byte
	participantID := a.participantID
	if len(participantID) != hex.EncodedLen(participantIDBytes) {
		logging.Errorf("enviar audio saliente: ID de participante inválido %q", participantID)
		return
	}
	if _, err := hex.Decode(senderID[:], []byte(participantID)); err != nil {
		logging.Errorf("enviar audio saliente: ID de participante inválido %q", participantID)
		return
	}
	encoder := a.outgoingEncoder()
	if encoder == nil {
		return
	}

	// sendMu ordena el envío si micrófono y celular coinciden un instante (al cambiar de
	// entrada): talkspurt, sendBuf y el encoder son de a uno.
	a.sendMu.Lock()
	defer a.sendMu.Unlock()
	now := time.Now()
	if now.Sub(a.lastAudioSent) > talkspurtGap {
		a.talkspurt++
	}
	a.lastAudioSent = now
	for _, samples := range frames {
		err := encoder.Encode(samples, func(opusPacket []byte) {
			a.sendBuf = append(a.sendBuf[:0], participantIDBytes)
			a.sendBuf = append(a.sendBuf, senderID[:]...)
			a.sendBuf = append(a.sendBuf, opusPacket...)
			packet := transport.Packet{
				Kind:      transport.PacketAudio,
				Flags:     transport.FlagAudioSequence,
				Sequence:  a.audioSequence.Add(1),
				Talkspurt: a.talkspurt,
				Payload:   a.sendBuf,
			}
			if host {
				udp.Broadcast(packet, nil)
				return
			}
			if err := udp.Send(peer, packet); err != nil {
				logging.Errorf("enviar audio saliente a %s: %s", peer, logging.FormatError(err))
			}
		})
		if err != nil {
			logging.Errorf("enviar audio saliente: %s", logging.FormatError(err))
			return
		}
	}
}

// outgoingEncoder devuelve el encoder Opus de la sala; si todavía no hay (el celular puede
// mandar antes de que startAudio termine de abrir malgo) lo crea.
func (a *App) outgoingEncoder() *audio.OpusEncoder {
	if encoder := a.encoder.Load(); encoder != nil {
		return encoder
	}
	encoder, err := audio.NewOpusEncoder()
	if err != nil {
		logging.Errorf("%s", logging.FormatError(err))
		return nil
	}
	if a.encoder.CompareAndSwap(nil, encoder) {
		return encoder
	}
	return a.encoder.Load()
}

// resetEncoder crea un encoder nuevo al conectar (estado limpio para la sala nueva).
func (a *App) resetEncoder() {
	encoder, err := audio.NewOpusEncoder()
	if err != nil {
		logging.Errorf("%s", logging.FormatError(err))
		a.encoder.Store(nil)
		return
	}
	a.encoder.Store(encoder)
}

// outputFilter es la configuración de la cadena de salida (Ajustes → Salida) que el Engine
// aplica por emisor. La llama el callback de playback: una carga atómica, sin locks.
func (a *App) outputFilter() (audio.FilterSettings, bool) {
	cfg := a.conf()
	return audio.FilterSettings{
		GainDB:           cfg.OutputGainDB,
		HighPassEnabled:  cfg.OutputHighPassEnabled,
		HighPassHz:       cfg.OutputHighPassHz,
		LowPassEnabled:   cfg.OutputLowPassEnabled,
		LowPassHz:        cfg.OutputLowPassHz,
		LimiterEnabled:   cfg.OutputLimiterEnabled,
		LimiterThreshold: cfg.OutputLimiterDB,
	}, cfg.OutputFilterEnabled
}

// outgoingSettings arma la configuración de la cadena de envío. Corre por frame (callback de
// captura): lee la configuración con una sola carga atómica, nunca espera a la UI.
func outgoingSettings(cfg *config.Config) (audio.NoiseGate, audio.FilterSettings) {
	return audio.NoiseGate{ThresholdDB: cfg.ThresholdDB}, audio.FilterSettings{
		RNNoiseEnabled:      cfg.RNNoiseEnabled,
		VADEnabled:          cfg.VADEnabled,
		NoiseGateEnabled:    cfg.NoiseGateEnabled,
		GainDB:              cfg.InputGainDB,
		HighPassEnabled:     cfg.HighPassEnabled,
		HighPassHz:          cfg.HighPassHz,
		LowPassEnabled:      cfg.LowPassEnabled,
		LowPassHz:           cfg.LowPassHz,
		NotchEnabled:        cfg.NotchEnabled,
		NotchHz:             cfg.NotchHz,
		CompressorEnabled:   cfg.CompressorEnabled,
		CompressorThreshold: cfg.CompressorThresholdDB,
		CompressorRatio:     cfg.CompressorRatio,
		ExpanderEnabled:     cfg.ExpanderEnabled,
		ExpanderThreshold:   cfg.ExpanderThresholdDB,
		ExpanderRatio:       cfg.ExpanderRatio,
		LimiterEnabled:      cfg.LimiterEnabled,
		LimiterThreshold:    cfg.LimiterThresholdDB,
		VADHoldMS:           cfg.VADHoldMS,
		GateHoldMS:          cfg.GateHoldMS,
	}
}

// prepareOutgoingFrames devuelve los frames a mandar (ver audio.PrepareOutgoingFrames). Valen
// hasta el próximo frame.
func (a *App) prepareOutgoingFrames(frame []byte) ([][]int16, bool) {
	samples, err := audio.DecodePCM(frame)
	if err != nil {
		logging.Errorf("procesar audio saliente: %s", logging.FormatError(err))
		return nil, false
	}
	cfg := a.conf()
	gate, settings := outgoingSettings(cfg)
	frames, _, err := audio.PrepareOutgoingFrames(samples, a.voiceProcessor, gate, cfg.AudioFilterEnabled, cfg.VADThreshold, settings, a.effects)
	if err != nil {
		logging.Errorf("procesar audio saliente: %s", logging.FormatError(err))
		return nil, false
	}
	return frames, len(frames) > 0
}

// prepareOutgoingPCM es la misma cadena en bytes (pre-roll concatenado), para la escucha de
// prueba de ajustes.
func (a *App) prepareOutgoingPCM(frame []byte) ([]byte, bool) {
	cfg := a.conf()
	gate, settings := outgoingSettings(cfg)
	payload, shouldSend, err := audio.PrepareOutgoingPCMWithFilters(frame, a.voiceProcessor, gate, cfg.AudioFilterEnabled, cfg.VADThreshold, settings, a.effects)
	if err != nil || !shouldSend {
		if err != nil {
			logging.Errorf("procesar audio saliente: %s", logging.FormatError(err))
		}
		return nil, false
	}
	return payload, true
}
