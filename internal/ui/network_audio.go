//go:build cgo

package ui

import (
	"encoding/hex"
	"net"
	"time"

	"voxmesh/internal/audio"
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

// talkspurtGap: si entre el último frame mandado y el próximo se capturó más que esto sin mandar
// nada (compuerta cerrada, mute, corte del celular), el próximo arranca una frase nueva
// (Packet.Talkspurt).
const talkspurtGap = 60 * time.Millisecond

// captureFrameDuration es lo que dura un frame capturado (20 ms).
const captureFrameDuration = time.Duration(audio.FrameSamples) * time.Second / audio.SampleRate

// audioTimeline numera el audio saliente (bajo sendMu). La secuencia avanza con cada paquete y
// también con los frames que la captura tuvo que tirar en medio de una frase: el receptor los ve
// como pérdidas y los oculta, en vez de recibir menos audio que tiempo (el buffer se le vaciaba y
// se cortaba). La frase cambia cuando lo capturado tuvo un hueco de más de talkspurtGap; se mide
// con la hora de captura, así un atraso al procesar (PC trabada) no parte la frase.
type audioTimeline struct {
	seq       uint32
	talkspurt uint8
	lastAt    time.Time // captura del último frame mandado
}

// next reserva count números de secuencia para un envío cuyo último frame se capturó en at, con
// dropped frames tirados por la captura justo antes. Devuelve el primero y la frase.
func (t *audioTimeline) next(count int, at time.Time, dropped int) (uint32, uint8) {
	if !t.lastAt.IsZero() && at.Sub(t.lastAt)-time.Duration(dropped)*captureFrameDuration <= talkspurtGap {
		t.seq += uint32(dropped)
	} else {
		t.talkspurt++
	}
	first := t.seq + 1
	t.seq += uint32(count)
	t.lastAt = at
	return first, t.talkspurt
}

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
	senderID := hex.EncodeToString(payload[1 : 1+idLen])
	if senderID == a.participantID {
		return // mi propia voz devuelta (dos hosts reenviándose entre sí): sería eco
	}
	if packet.Flags&transport.FlagShout != 0 {
		a.markPeerShouting(senderID)
	}
	if engine := a.engine(); engine != nil {
		engine.PlayStreamSeq(senderID, packet.Sequence, packet.Talkspurt, opusPacket)
	}
}

// sendOutgoingPCM corre la cadena de envío sobre un frame capturado y manda lo que corresponda
// (nada con la compuerta cerrada; al abrirse, el pre-roll y el frame actual), codificado en Opus.
// El rol se mira en cada frame (host: a todos; cliente: a su host), así la migración de host no
// reinicia el audio.
//
// La cancelación de eco va primero de todo, sobre el frame en el lugar: la ganancia, RNNoise, la
// línea seca del modo grito y el medidor ya ven la voz sin lo que sonó por los parlantes.
func (a *App) sendOutgoingPCM(frame audio.CapturedFrame, udp *transport.UDP) {
	if engine := a.engine(); engine != nil {
		if err := engine.CancelEcho(frame.PCM, frame.At, audio.InputEchoSettings(a.conf(), frame.Phone)); err != nil {
			logging.Errorf("%s", logging.FormatError(err))
		}
	}
	frames, ok, postDB := a.prepareOutgoingFrames(frame.PCM)
	a.reportMeter(frame.PCM, ok, postDB)
	// Mute: la cadena sigue corriendo (RNNoise retiene 20 ms y el VAD su hold; saltearla dejaría
	// audio viejo que saldría al desmutear) pero el frame se descarta igual que con la compuerta
	// cerrada (DTX): sin paquete, sin monitoreo ni indicador de voz. La secuencia no avanza, así
	// que el jitter buffer del receptor lo ve como silencio, no como pérdida.
	if !ok || len(frames) == 0 || a.isMuted.Load() {
		return
	}
	// Modo grito: aro rojo en el avatar propio, y viaja como flag en los paquetes de este frame
	// para que los demás lo vean.
	flags := transport.FlagAudioSequence
	if a.effects.Shouting() {
		flags |= transport.FlagShout
		a.markPeerShouting(a.participantID)
	}
	if engine := a.engine(); engine != nil && a.conf().LiveMonitoring {
		for _, samples := range frames {
			engine.Play(audio.EncodePCM(samples))
		}
	}
	a.flashVoiceIndicator()

	host := a.hostMode.Load()
	var peer *net.UDPAddr
	if !host {
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
	opusSettings := audio.EncoderSettings(a.conf())

	// sendMu ordena el envío si micrófono y celular coinciden un instante (al cambiar de
	// entrada): la línea de tiempo, sendBuf y el encoder son de a uno.
	a.sendMu.Lock()
	defer a.sendMu.Unlock()
	seq, talkspurt := a.timeline.next(len(frames), frame.At, frame.Dropped)
	for _, samples := range frames {
		err := encoder.Encode(samples, opusSettings, func(opusPacket []byte) {
			a.sendBuf = append(a.sendBuf[:0], participantIDBytes)
			a.sendBuf = append(a.sendBuf, senderID[:]...)
			a.sendBuf = append(a.sendBuf, opusPacket...)
			packet := transport.Packet{
				Kind:      transport.PacketAudio,
				Flags:     flags,
				Sequence:  seq,
				Talkspurt: talkspurt,
				Payload:   a.sendBuf,
			}
			seq++
			a.audioSent.Add(1)
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
	encoder, err := audio.NewOpusEncoder(audio.EncoderSettings(a.conf()))
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
	encoder, err := audio.NewOpusEncoder(audio.EncoderSettings(a.conf()))
	if err != nil {
		logging.Errorf("%s", logging.FormatError(err))
		a.encoder.Store(nil)
		return
	}
	a.encoder.Store(encoder)
}

// playbackSettings es la configuración de salida (Ajustes → Salida: filtro por emisor, jitter
// buffer, limitador de la mezcla) que lee el Engine. La llama el callback de playback: una carga
// atómica, sin locks.
func (a *App) playbackSettings() audio.PlaybackSettings {
	return audio.OutputSettings(a.conf())
}

// prepareOutgoingFrames devuelve los frames a mandar (ver audio.PrepareOutgoingFrames). Valen
// hasta el próximo frame.
//
// postDB es el nivel del frame ya procesado (lo que se manda, o se mandaría si la compuerta
// estuviera abierta), para el medidor post-filtro.
func (a *App) prepareOutgoingFrames(frame []byte) (frames [][]int16, ok bool, postDB float32) {
	samples, err := audio.DecodePCM(frame)
	if err != nil {
		logging.Errorf("procesar audio saliente: %s", logging.FormatError(err))
		return nil, false, audio.LevelFloorDB
	}
	cfg := a.conf()
	gate, settings := audio.InputSettings(cfg)
	frames, _, err = audio.PrepareOutgoingFrames(samples, a.voiceProcessor, gate, cfg.AudioFilterEnabled, cfg.VADThreshold, settings, a.effects)
	if err != nil {
		logging.Errorf("procesar audio saliente: %s", logging.FormatError(err))
		return nil, false, audio.LevelFloorDB
	}
	// La cadena procesa samples en el lugar: después de la llamada es el frame filtrado.
	return frames, len(frames) > 0, audio.SamplesLevel(samples)
}

// prepareOutgoingPCM es la misma cadena en bytes (pre-roll concatenado), para la escucha de
// prueba de ajustes. Le avisa al medidor (reportMeter) aunque el frame no salga.
func (a *App) prepareOutgoingPCM(frame []byte) ([]byte, bool) {
	frames, ok, postDB := a.prepareOutgoingFrames(frame)
	a.reportMeter(frame, ok, postDB)
	if !ok {
		return nil, false
	}
	total := 0
	for _, samples := range frames {
		total += len(samples)
	}
	joined := make([]int16, 0, total)
	for _, samples := range frames {
		joined = append(joined, samples...)
	}
	return audio.EncodePCM(joined), true
}
