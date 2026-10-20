//go:build cgo

package ui

import (
	"encoding/binary"
	"net"
	"runtime"
	"runtime/debug"
	"strconv"

	"voxmesh/internal/assets"
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
	server, err := phonemic.Start(a.cfg.PhoneMicToken, a.cfg.PhoneMicPort, a.cfg.PhoneMicBufferEnabled, a.cfg.PhoneMicBufferMS, a.cfg.Username, a.receivePhonePCM, a.setStatus)
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
	a.cfg.PhoneMicToken = server.Token()
	a.cfg.PhoneMicPort = server.Port()
	if err := config.Save(a.cfgPath, a.cfg); err != nil {
		logging.Errorf("guardar enlace de micrófono de celular: %s", logging.FormatError(err))
	}
	if a.cfg.PhoneMicBufferEnabled {
		a.setStatus("Micrófono de celular listo con " + strconv.Itoa(a.cfg.PhoneMicBufferMS) + " ms de buffer. Escaneá el QR desde la misma Wi-Fi.")
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
	if a.cfg.InputDevice == phoneMicInput {
		a.cfg.InputDevice = "Sistema predeterminado"
	}
}

func (a *App) receivePhonePCM(frame []byte) {
	a.playPhonePreview(frame)
	if !a.phoneMicEnabled() || a.transport == nil {
		return
	}
	var peer *net.UDPAddr
	if !a.hostMode.Load() {
		var err error
		peer, err = net.ResolveUDPAddr("udp", a.endpoint)
		if err != nil {
			logging.Errorf("resolver host para micrófono de celular: %s", logging.FormatError(err))
			return
		}
	}
	a.sendOutgoingPCM(frame, a.transport, peer, a.hostMode.Load())
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
	a.previewMu.Lock()
	player := a.previewPlayer
	engine := a.previewEngine
	a.previewPlayer = nil
	a.previewEngine = nil
	a.previewMu.Unlock()
	if engine != nil {
		engine.Stop()
	}
	if player != nil {
		player.Stop()
	}
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
	if a.cfg.SoundboardMuted || a.historyStore == nil {
		return
	}
	go func() {
		data, readErr := a.historyStore.ReadSound(sound.ID, sound.Ext)
		if readErr == nil {
			a.playSoundPCM(data)
			return
		}
		a.soundMu.Lock()
		if a.pendingPlays == nil {
			a.pendingPlays = make(map[string]struct{})
		}
		a.pendingPlays[sound.ID] = struct{}{}
		a.soundMu.Unlock()
		if a.transport == nil || a.roomState.HostID == a.participantID {
			return
		}
		peer, resolveErr := net.ResolveUDPAddr("udp", a.endpoint)
		if resolveErr != nil {
			return
		}
		request := assets.MissingRequest{Kind: assets.KindSound, ID: sound.ID, Ext: sound.Ext}
		_ = a.transport.Send(peer, transport.Packet{Kind: transport.PacketAssetMissing, Sequence: a.sequence.Add(1), Payload: assets.EncodeMissing(request)})
	}()
}

func (a *App) ensureSoundPlayer() *audio.Player {
	a.soundMu.Lock()
	defer a.soundMu.Unlock()
	if a.soundPlayer != nil {
		return a.soundPlayer
	}
	player, err := audio.NewPlayer(a.cfg.OutputDevice)
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
	vol := a.cfg.SoundboardVolume
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
	if !a.cfg.EventSoundsEnabled || len(sfx) == 0 {
		return
	}
	if a.audioEngine != nil {
		a.audioEngine.Play(sfx)
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
	go func() {
		runtime.GC()
		debug.FreeOSMemory()
	}()
}

func (a *App) startAudio(udp *transport.UDP, peer *net.UDPAddr, host bool) {
	inputDevice := a.cfg.InputDevice
	if inputDevice == phoneMicInput {
		inputDevice = "Sistema predeterminado"
	}
	engine, err := audio.NewEngine(inputDevice, a.cfg.OutputDevice, func(frame []byte) {
		if a.phoneMicEnabled() {
			return
		}
		a.sendOutgoingPCM(frame, udp, peer, host)
	})
	if err != nil {
		a.setStatus("Audio no disponible: " + err.Error())
		return
	}
	if a.cfg.JitterBufferMS >= 40 {
		engine.SetJitterBufferMS(a.cfg.JitterBufferMS)
	}
	a.audioEngine = engine
	a.setStatus("Audio iniciado correctamente")
}

func (a *App) restartAudioInRoom() {
	if a.audioEngine != nil {
		a.audioEngine.Stop()
		a.audioEngine = nil
	}
	if a.transport != nil {
		a.setStatus("Reiniciando audio en la sala...")
		var peer *net.UDPAddr
		if !a.hostMode.Load() && a.endpoint != "" {
			peer, _ = net.ResolveUDPAddr("udp", a.endpoint)
		}
		go a.startAudio(a.transport, peer, a.hostMode.Load())
	} else {
		a.setStatus("No estas conectado a ningún servidor")
	}
}
