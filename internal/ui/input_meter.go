//go:build cgo

package ui

import (
	"voxmesh/internal/audio"
)

// inputMeter es lo que el medidor de Ajustes sabe de un frame de entrada ya procesado por la
// cadena de envío: no solo el volumen, sino qué se hizo con él.
type inputMeter struct {
	db      float32 // RMS de la entrada en dBFS (la escala del umbral del modo grito)
	postDB  float32 // RMS del frame después de la cadena (la escala del volumen mínimo de la puerta)
	vad     float32 // probabilidad de voz que dio RNNoise (0–1); -1 sin RNNoise
	clipped bool    // algún pico saturó (aviso "SATURA")
	sent    bool    // la cadena lo manda (o lo mandaría, fuera de la sala)
	shout   bool    // salió en modo grito
	muted   bool    // en sala y muteado: no se manda aunque la cadena lo deje pasar
}

// reportMeter le pasa al medidor de Ajustes (si está abierto) el resultado de un frame que ya
// pasó por la cadena de envío. pcm es la entrada sin procesar; postDB, el nivel ya procesado.
func (a *App) reportMeter(pcm []byte, sent bool, postDB float32) {
	listener := a.levelListener.Load()
	if listener == nil {
		return
	}
	db, clipped := audio.FrameLevel(pcm)
	muted := a.isMuted.Load() && a.connected()
	(*listener)(inputMeter{
		db:      db,
		postDB:  postDB,
		vad:     a.effects.VAD(),
		clipped: clipped,
		sent:    sent && !muted,
		shout:   sent && !muted && a.effects.Shouting(),
		muted:   muted,
	})
}

// meterFrame corre la cadena de envío sobre un frame solo para el medidor (sin mandar ni
// reproducir nada): micrófono fuera de la sala y celular sin sala ni escucha de prueba.
func (a *App) meterFrame(pcm []byte) {
	if a.levelListener.Load() == nil {
		return
	}
	_, sent, postDB := a.prepareOutgoingFrames(pcm)
	a.reportMeter(pcm, sent, postDB)
}
