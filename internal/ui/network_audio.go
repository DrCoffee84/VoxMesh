//go:build cgo

package ui

import (
	"net"
	"time"

	"voxmesh/internal/audio"
	"voxmesh/internal/logging"
	"voxmesh/internal/transport"
)

func (a *App) isAudioSequenceValid(senderID string, seq uint32) bool {
	a.audioSeqMu.Lock()
	defer a.audioSeqMu.Unlock()

	lastSeq, exists := a.peerAudioSeq[senderID]
	lastTime := a.peerAudioTime[senderID]
	now := time.Now()

	if !exists || now.Sub(lastTime) > 800*time.Millisecond {
		a.peerAudioSeq[senderID] = seq
		a.peerAudioTime[senderID] = now
		return true
	}

	diff := int32(seq - lastSeq)
	if diff <= 0 {
		return false // Discard duplicate or out-of-order late arrival
	}

	a.peerAudioSeq[senderID] = seq
	a.peerAudioTime[senderID] = now
	return true
}

// handleAudioPacket es el único punto de entrada del audio recibido por red:
// separa el ID del emisor del PCM, valida la secuencia y lo entrega al Engine.
// Lo usan las dos ramas de receiveLoop (host y cliente); el reenvío a otros
// peers lo sigue haciendo receiveLoop en modo host.
func (a *App) handleAudioPacket(packet transport.Packet, addr *net.UDPAddr) {
	senderID := ""
	pcm := packet.Payload
	if len(packet.Payload) > 1 && int(packet.Payload[0]) > 0 && len(packet.Payload) > 1+int(packet.Payload[0]) {
		idLen := int(packet.Payload[0])
		senderID = string(packet.Payload[1 : 1+idLen])
		pcm = packet.Payload[1+idLen:]
	}
	if len(pcm) < 960 || len(pcm)%2 != 0 {
		return
	}

	// Emisor con secuencia de audio propia: el jitter buffer del Engine reordena,
	// descarta los tardíos y oculta las pérdidas, así que no filtramos acá.
	if packet.Flags&transport.FlagAudioSequence != 0 {
		a.markPeerSpeaking(senderID, addr.String())
		if a.audioEngine != nil {
			a.audioEngine.PlayStreamSeq(senderID, packet.Sequence, a.prepareIncomingPCM(pcm))
		}
		return
	}

	// Emisor de una versión anterior (secuencia global): camino legacy sin reordenamiento.
	if a.isAudioSequenceValid(senderID, packet.Sequence) {
		a.markPeerSpeaking(senderID, addr.String())
		if a.audioEngine != nil {
			a.audioEngine.PlayStream(senderID, a.prepareIncomingPCM(pcm))
		}
	}
}

func (a *App) sendOutgoingPCM(frame []byte, udp *transport.UDP, peer *net.UDPAddr, host bool) {
	payload, shouldSend := a.prepareOutgoingPCM(frame)
	if !shouldSend {
		return
	}
	if a.cfg.LiveMonitoring && a.audioEngine != nil {
		a.audioEngine.Play(payload)
	}
	a.flashVoiceIndicator()
	idBytes := []byte(a.participantID)
	audioPayload := make([]byte, 1+len(idBytes)+len(payload))
	audioPayload[0] = byte(len(idBytes))
	copy(audioPayload[1:], idBytes)
	copy(audioPayload[1+len(idBytes):], payload)
	packet := transport.Packet{
		Kind:     transport.PacketAudio,
		Flags:    transport.FlagAudioSequence,
		Sequence: a.audioSequence.Add(1),
		Payload:  audioPayload,
	}
	if host {
		udp.Broadcast(packet, nil)
		return
	}
	if peer == nil || peer.IP == nil || peer.IP.IsUnspecified() {
		if a.endpoint != "" {
			var err error
			peer, err = net.ResolveUDPAddr("udp", a.endpoint)
			if err != nil || peer.IP == nil || peer.IP.IsUnspecified() {
				return
			}
		} else {
			logging.Errorf("enviar audio saliente: host no disponible")
			return
		}
	}
	if err := udp.Send(peer, packet); err != nil {
		logging.Errorf("enviar audio saliente a %s: %s", peer, logging.FormatError(err))
	}
}

func (a *App) prepareIncomingPCM(frame []byte) []byte {
	if !a.cfg.OutputFilterEnabled {
		return frame
	}
	settings := audio.FilterSettings{
		GainDB:           a.cfg.OutputGainDB,
		HighPassEnabled:  a.cfg.OutputHighPassEnabled,
		HighPassHz:       a.cfg.OutputHighPassHz,
		LowPassEnabled:   a.cfg.OutputLowPassEnabled,
		LowPassHz:        a.cfg.OutputLowPassHz,
		LimiterEnabled:   a.cfg.OutputLimiterEnabled,
		LimiterThreshold: a.cfg.OutputLimiterDB,
	}
	filtered, err := a.outputEffects.Apply(frame, settings)
	if err != nil {
		logging.Errorf("procesar audio entrante: %s", logging.FormatError(err))
		return frame
	}
	return filtered
}

func (a *App) prepareOutgoingPCM(frame []byte) ([]byte, bool) {
	gate := audio.NoiseGate{ThresholdDB: a.cfg.ThresholdDB}
	settings := audio.FilterSettings{
		RNNoiseEnabled:      a.cfg.RNNoiseEnabled,
		VADEnabled:          a.cfg.VADEnabled,
		NoiseGateEnabled:    a.cfg.NoiseGateEnabled,
		GainDB:              a.cfg.InputGainDB,
		HighPassEnabled:     a.cfg.HighPassEnabled,
		HighPassHz:          a.cfg.HighPassHz,
		LowPassEnabled:      a.cfg.LowPassEnabled,
		LowPassHz:           a.cfg.LowPassHz,
		NotchEnabled:        a.cfg.NotchEnabled,
		NotchHz:             a.cfg.NotchHz,
		CompressorEnabled:   a.cfg.CompressorEnabled,
		CompressorThreshold: a.cfg.CompressorThresholdDB,
		CompressorRatio:     a.cfg.CompressorRatio,
		ExpanderEnabled:     a.cfg.ExpanderEnabled,
		ExpanderThreshold:   a.cfg.ExpanderThresholdDB,
		ExpanderRatio:       a.cfg.ExpanderRatio,
		LimiterEnabled:      a.cfg.LimiterEnabled,
		LimiterThreshold:    a.cfg.LimiterThresholdDB,
		VADHoldMS:           a.cfg.VADHoldMS,
		GateHoldMS:          a.cfg.GateHoldMS,
	}
	payload, shouldSend, err := audio.PrepareOutgoingPCMWithFilters(frame, a.voiceProcessor, gate, a.cfg.AudioFilterEnabled, a.cfg.VADThreshold, settings, a.effects)
	if err != nil || !shouldSend {
		if err != nil {
			logging.Errorf("procesar audio saliente: %s", logging.FormatError(err))
		}
		return nil, false
	}
	return payload, true
}
