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
	previewed := a.playPhonePreview(frame)
	udp := a.transport.Load()
	if udp == nil || !a.phoneMicEnabled() {
		if !previewed {
			a.meterFrame(frame) // sin sala ni escucha de prueba: solo para el medidor
		}
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
func (a *App) startMicPreview(input, output audio.Device) error {
	if input.Name == phoneMicInput {
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

// playPhonePreview reproduce el frame del celular procesado si la escucha de prueba está activa.
// Devuelve si lo procesó.
func (a *App) playPhonePreview(frame []byte) bool {
	a.previewMu.RLock()
	player := a.previewPlayer
	a.previewMu.RUnlock()
	if player == nil {
		return false
	}
	payload, shouldPlay := a.prepareOutgoingPCM(frame)
	if shouldPlay {
		player.Play(payload)
	}
	return true
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
	player, err := audio.NewPlayer(outputChoice(a.conf()))
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
	inputWant, outputWant := inputChoice(cfg), outputChoice(cfg)
	a.resetEncoder()
	a.effects.Reset()
	a.voiceProcessor.Reset()
	engine, err := audio.NewEngine(inputWant, outputWant, func(frame audio.CapturedFrame) {
		if a.phoneMicEnabled() {
			return
		}
		a.sendOutgoingPCM(frame, udp)
	})
	if err != nil {
		logging.Errorf("audio no disponible: %s", logging.FormatError(err))
		a.setStatus("Audio no disponible: " + err.Error() + ". Probá con Refresh Audio.")
		return
	}
	engine.SetPlaybackSettings(a.playbackSettings)
	a.applyPeerVolumes(engine)
	input, output := engine.Devices()
	notice := a.applyResolvedDevices(inputWant, outputWant, input, output)
	if !a.publishEngine(engine, udp) {
		return
	}
	a.setStatus(notice + "Audio listo: " + audioDevicesText(cfg, input, output))
}

// inputChoice es el micrófono elegido en la config. El celular no es un dispositivo de Windows:
// con él la sala abre el predeterminado (su captura se ignora, ver startAudio).
func inputChoice(cfg *config.Config) audio.Device {
	if cfg.InputDevice == phoneMicInput {
		return audio.DefaultChoice
	}
	return savedInput(cfg)
}

// savedInput es la entrada guardada tal cual (el celular incluido), para Ajustes.
func savedInput(cfg *config.Config) audio.Device {
	return audio.Device{Name: cfg.InputDevice, ID: cfg.InputDeviceID}
}

func outputChoice(cfg *config.Config) audio.Device {
	return audio.Device{Name: cfg.OutputDevice, ID: cfg.OutputDeviceID}
}

// applyResolvedDevices guarda lo que se abrió para cada elección: si el elegido ya no estaba queda
// el predeterminado de Windows (no se recuerda: el próximo cambio de dispositivo lo resuelve
// Windows); si estaba, se actualiza su nombre si Windows lo cambió. Solo
// pisa una elección que no cambió mientras se abría el audio. Devuelve el aviso para la barra de
// estado ("" si no faltó ninguno).
func (a *App) applyResolvedDevices(inputWant, outputWant audio.Device, input, output audio.Resolved) string {
	before := a.conf()
	after := a.updateConfig(func(c *config.Config) {
		if c.InputDevice != phoneMicInput && c.InputDevice == inputWant.Name && c.InputDeviceID == inputWant.ID {
			c.InputDevice, c.InputDeviceID = input.Device.Name, input.Device.ID
		}
		if c.OutputDevice == outputWant.Name && c.OutputDeviceID == outputWant.ID {
			c.OutputDevice, c.OutputDeviceID = output.Device.Name, output.Device.ID
		}
	})
	if after.InputDevice != before.InputDevice || after.InputDeviceID != before.InputDeviceID ||
		after.OutputDevice != before.OutputDevice || after.OutputDeviceID != before.OutputDeviceID {
		a.saveConfig()
	}
	notice := ""
	if input.Missing && before.InputDevice != phoneMicInput {
		logging.Errorf("micrófono %q no encontrado: se usa el predeterminado de Windows", inputWant.Name)
		notice += "No encontré el micrófono «" + inputWant.Name + "»: uso el predeterminado de Windows. "
	}
	if output.Missing {
		logging.Errorf("salida %q no encontrada: se usa la predeterminada de Windows", outputWant.Name)
		notice += "No encontré la salida «" + outputWant.Name + "»: uso la predeterminada de Windows. "
	}
	return notice
}

// audioDevicesText dice por dónde entra y sale el audio, para la barra de estado.
func audioDevicesText(cfg *config.Config, input, output audio.Resolved) string {
	mic := deviceLabel(input.Device)
	if cfg.InputDevice == phoneMicInput {
		mic = "celular"
	}
	return "micrófono «" + mic + "», salida «" + deviceLabel(output.Device) + "»."
}

func deviceLabel(device audio.Device) string {
	if audio.IsDefaultDevice(device.Name) {
		return "predeterminado de Windows"
	}
	return device.Name
}

// dropSoundPlayer cierra la salida del soundboard; el próximo sonido la abre de nuevo con el
// dispositivo vigente. Lento (libera malgo): fuera del hilo de UI.
func (a *App) dropSoundPlayer() {
	a.soundMu.Lock()
	player := a.soundPlayer
	a.soundPlayer = nil
	a.soundMu.Unlock()
	if player != nil {
		player.Stop()
	}
}

// restartAudioInRoom es "Refresh Audio" (y Guardar en Ajustes con otro dispositivo): vuelve a
// pedirle a Windows los dispositivos y abre el elegido si sigue, si no el predeterminado. Corre
// fuera del hilo de UI.
func (a *App) restartAudioInRoom() {
	a.stopEngine()
	a.dropSoundPlayer()
	if udp := a.transport.Load(); udp != nil {
		a.setStatus("Recargando el audio...")
		go a.startAudio(udp)
	} else {
		cfg := a.conf()
		inputWant, outputWant := inputChoice(cfg), outputChoice(cfg)
		input, output, err := audio.ResolveDevices(inputWant, outputWant)
		if err != nil {
			logging.Errorf("recargar audio: %s", logging.FormatError(err))
		}
		notice := a.applyResolvedDevices(inputWant, outputWant, input, output)
		a.setStatus(notice + "Audio recargado: " + audioDevicesText(cfg, input, output))
	}
}
