//go:build cgo

package ui

import (
	"encoding/binary"
	"strconv"
	"time"

	"voxmesh/internal/audio"
	"voxmesh/internal/config"
	"voxmesh/internal/logging"
	"voxmesh/internal/phonemic"
	"voxmesh/internal/room"
	"voxmesh/internal/transport"
)

func (a *App) ensurePhoneMicServer() (*phonemic.Server, error) {
	a.phoneMicMu.RLock()
	server := a.phoneMic
	a.phoneMicMu.RUnlock()
	if server != nil {
		return server, nil
	}
	cfg := a.conf()
	buffer := phonemic.Buffer{
		Enabled:          cfg.PhoneMicBufferEnabled,
		MS:               cfg.PhoneMicBufferMS,
		DriftSlackFrames: cfg.PhoneDriftSlackMS / 20,
		DriftForceFrames: cfg.PhoneDriftForceMS / 20,
	}
	server, err := phonemic.Start(cfg.PhoneMicToken, cfg.PhoneMicPort, buffer, cfg.Username, a.receivePhonePCM, a.setStatus)
	if err != nil {
		return nil, err
	}
	a.phoneMicMu.Lock()
	if a.phoneMic == nil {
		a.phoneMic = server
	} else {
		server.Stop()
		server = a.phoneMic
	}
	a.phoneMicMu.Unlock()
	token, port := server.Token(), server.Port()
	cfg = a.updateConfig(func(c *config.Config) {
		c.PhoneMicToken = token
		c.PhoneMicPort = port
	})
	a.saveConfig()
	if cfg.PhoneMicBufferEnabled {
		a.setStatus("Micrófono de celular listo con " + strconv.Itoa(cfg.PhoneMicBufferMS) + " ms de buffer. Escaneá el QR desde la misma Wi-Fi.")
	} else {
		a.setStatus("Micrófono de celular listo sin buffer. Escaneá el QR desde la misma Wi-Fi.")
	}
	return server, nil
}

func (a *App) stopPhoneMic() {
	a.phoneMicMu.Lock()
	server := a.phoneMic
	a.phoneMic = nil
	a.phoneMicMu.Unlock()
	if server != nil {
		server.Stop()
	}
}

// receivePhonePCM recibe cada frame del celular con su hora en el horario del servidor (at): un
// atraso al repartir no parece una pausa.
func (a *App) receivePhonePCM(frame []byte, at time.Time) {
	a.playPhonePreview(frame)
	udp := a.transport.Load()
	if udp == nil || !a.phoneMicEnabled() {
		return
	}
	a.sendOutgoingPCM(audio.CapturedFrame{PCM: frame, At: at}, udp)
}

func (a *App) phoneMicEnabled() bool {
	a.phoneMicMu.RLock()
	defer a.phoneMicMu.RUnlock()
	return a.phoneMic != nil
}

func (a *App) setPreviewPlayer(player *audio.Player) {
	a.previewMu.Lock()
	previous := a.previewPlayer
	a.previewPlayer = player
	a.previewMu.Unlock()
	if previous != nil {
		previous.Stop()
	}
}

func (a *App) stopPreviewAudio() {
	a.detachPreviewAudio()()
}

// detachPreviewAudio saca el engine/player de la escucha de prueba bajo lock (rápido) y devuelve
// la función que los detiene (lenta: libera dispositivos malgo), para correrla fuera del hilo de UI
// sin que un stop tardío alcance a un preview nuevo.
func (a *App) detachPreviewAudio() func() {
	a.previewMu.Lock()
	player := a.previewPlayer
	engine := a.previewEngine
	a.previewPlayer = nil
	a.previewEngine = nil
	a.previewMu.Unlock()
	return func() {
		if engine != nil {
			engine.Stop()
		}
		if player != nil {
			player.Stop()
		}
	}
}

// startMicPreview abre la escucha de prueba del micrófono (pantalla de ajustes). Abre
// dispositivos malgo, así que se llama fuera del hilo de UI.
func (a *App) startMicPreview(input, output string) error {
	if input == phoneMicInput {
		player, err := audio.NewPlayer(output)
		if err != nil {
			return err
		}
		a.setPreviewPlayer(player)
		return nil
	}
	var engine *audio.Engine
	var err error
	engine, err = audio.NewEngine(input, output, func(frame audio.CapturedFrame) {
		payload, shouldPlay := a.prepareOutgoingPCM(frame.PCM)
		if shouldPlay && engine != nil {
			engine.Play(payload)
		}
	})
	if err != nil {
		return err
	}
	a.previewMu.Lock()
	a.previewEngine = engine
	a.previewMu.Unlock()
	return nil
}

func (a *App) playPhonePreview(frame []byte) {
	a.previewMu.RLock()
	player := a.previewPlayer
	a.previewMu.RUnlock()
	if player == nil {
		return
	}
	payload, shouldPlay := a.prepareOutgoingPCM(frame)
	if shouldPlay {
		player.Play(payload)
	}
}

func (a *App) playIncomingSound(sound room.Sound) {
	if a.conf().SoundboardMuted || a.historyStore == nil {
		return
	}
	go a.playOrFetchSound(sound)
}

func (a *App) ensureSoundPlayer() *audio.Player {
	a.soundMu.Lock()
	defer a.soundMu.Unlock()
	if a.soundPlayer != nil {
		return a.soundPlayer
	}
	player, err := audio.NewPlayer(a.conf().OutputDevice)
	if err != nil {
		logging.Errorf("iniciar salida de sonidos: %s", logging.FormatError(err))
		return nil
	}
	a.soundPlayer = player
	return player
}

func applyVolume(pcm []byte, volume float32) []byte {
	if volume >= 0.99 && volume <= 1.01 {
		return pcm
	}
	if volume <= 0.01 {
		for i := range pcm {
			pcm[i] = 0
		}
		return pcm
	}
	for i := 0; i+1 < len(pcm); i += 2 {
		sample := int16(binary.LittleEndian.Uint16(pcm[i : i+2]))
		scaled := int32(float32(sample) * volume)
		if scaled > 32767 {
			scaled = 32767
		} else if scaled < -32768 {
			scaled = -32768
		}
		binary.LittleEndian.PutUint16(pcm[i:i+2], uint16(int16(scaled)))
	}
	return pcm
}

func (a *App) playSoundPCM(data []byte) {
	vol := a.conf().SoundboardVolume
	if vol <= 0.01 {
		vol = 1.0
	}
	scaled := applyVolume(data, vol)
	if player := a.ensureSoundPlayer(); player != nil {
		player.Clear()
		player.Play(scaled)
	}
}

func (a *App) playEventSound(sfx []byte) {
	if len(sfx) == 0 || !a.conf().EventSoundsEnabled {
		return
	}
	if engine := a.engine(); engine != nil {
		engine.Play(sfx)
	} else if player := a.ensureSoundPlayer(); player != nil {
		player.Play(sfx)
	}
}

func (a *App) stopSoundboard() {
	a.soundMu.RLock()
	player := a.soundPlayer
	a.soundMu.RUnlock()
	if player != nil {
		player.Clear()
	}
}

// startAudio abre los dispositivos de la sala (lento: correr fuera del hilo de UI y de
// receiveLoop) y publica el Engine para la conexión udp. Sirve para cualquier rol: el envío mira
// si es host o cliente en cada frame.
func (a *App) startAudio(udp *transport.UDP) {
	cfg := a.conf()
	inputDevice := cfg.InputDevice
	if inputDevice == phoneMicInput {
		inputDevice = config.DefaultDevice
	}
	a.resetEncoder()
	engine, err := audio.NewEngine(inputDevice, cfg.OutputDevice, func(frame audio.CapturedFrame) {
		if a.phoneMicEnabled() {
			return
		}
		a.sendOutgoingPCM(frame, udp)
	})
	if err != nil {
		a.setStatus("Audio no disponible: " + err.Error())
		return
	}
	engine.SetPlaybackSettings(a.playbackSettings)
	a.applyPeerVolumes(engine)
	if !a.publishEngine(engine, udp) {
		return
	}
	a.setStatus("Audio iniciado correctamente")
}

func (a *App) restartAudioInRoom() {
	a.stopEngine()
	if udp := a.transport.Load(); udp != nil {
		a.setStatus("Reiniciando audio en la sala...")
		go a.startAudio(udp)
	} else {
		a.setStatus("No estas conectado a ningún servidor")
	}
}
