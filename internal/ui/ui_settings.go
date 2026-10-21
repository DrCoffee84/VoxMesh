//go:build cgo

package ui

import (
	"fmt"
	"image/color"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"voxmesh/internal/audio"
	"voxmesh/internal/config"
	"voxmesh/internal/logging"
)

func (a *App) showSettings() {
	a.setStatus("Cargando opciones y dispositivos de audio...")
	go func() {
		devices, err := audio.ListDevices()
		if err != nil {
			fyne.Do(func() {
				a.setStatus("No se pudieron enumerar los dispositivos: " + err.Error())
			})
			return
		}
		fyne.Do(func() {
			a.renderSettingsView(devices)
		})
	}()
}

func (a *App) renderSettingsView(devices audio.DeviceLists) {
	inputOptions := append([]string{"Sistema predeterminado", phoneMicInput}, devices.Inputs...)
	outputOptions := append([]string{"Sistema predeterminado"}, devices.Outputs...)
	// cfg es la instantánea al abrir la vista: solo para valores iniciales de los widgets. Los
	// callbacks publican con updateConfig y, si necesitan el valor vigente, leen a.conf().
	cfg := a.conf()
	openedInput, openedOutput := cfg.InputDevice, cfg.OutputDevice
	// baseline es LiveSettings al abrir: mientras la vista esté abierta writeConfig persiste esto
	// (no el borrador en vivo) y discardSettings lo restaura. Los campos de texto (nombre, puertos)
	// no se publican hasta Guardar.
	baseline := cfg.LiveSettings
	a.cfgBaseline.Store(&baseline)
	a.phoneMicMu.RLock()
	phoneWasRunning := a.phoneMic != nil
	a.phoneMicMu.RUnlock()
	username := widget.NewEntry()
	username.SetText(cfg.Username)
	input := widget.NewSelect(inputOptions, nil)
	input.SetSelected(cfg.InputDevice)
	output := widget.NewSelect(outputOptions, nil)
	output.SetSelected(cfg.OutputDevice)
	sensitivity := widget.NewSlider(0, 1)
	sensitivity.Step = 0.05
	sensitivity.Value = float64(cfg.VADThreshold)
	thresholdLabel := widget.NewLabel(fmt.Sprintf("Sensibilidad VAD: %.2f", cfg.VADThreshold))
	sensitivity.OnChanged = func(value float64) {
		a.updateConfig(func(c *config.Config) { c.VADThreshold = float32(value) })
		thresholdLabel.SetText(fmt.Sprintf("Sensibilidad VAD: %.2f", value))
	}
	mode := widget.NewRadioGroup([]string{"Activación por voz", "Push-to-talk"}, func(selected string) {
		if selected == "Push-to-talk" {
			a.updateConfig(func(c *config.Config) { c.MicMode = config.PushToTalk })
		} else {
			a.updateConfig(func(c *config.Config) { c.MicMode = config.VoiceActivation })
		}
	})
	if cfg.MicMode == config.PushToTalk {
		mode.SetSelected("Push-to-talk")
	} else {
		mode.SetSelected("Activación por voz")
	}
	filterCheck := widget.NewCheck("Aplicar filtro de voz", func(checked bool) { a.updateConfig(func(c *config.Config) { c.AudioFilterEnabled = checked }) })
	filterCheck.SetChecked(cfg.AudioFilterEnabled)
	rnnoiseCheck := widget.NewCheck("Supresión de ruido (RNNoise)", func(checked bool) { a.updateConfig(func(c *config.Config) { c.RNNoiseEnabled = checked }) })
	rnnoiseCheck.SetChecked(cfg.RNNoiseEnabled)
	vadCheck := widget.NewCheck("Detección de voz (VAD)", func(checked bool) { a.updateConfig(func(c *config.Config) { c.VADEnabled = checked }) })
	vadCheck.SetChecked(cfg.VADEnabled)
	gateCheck := widget.NewCheck("Puerta de ruido", func(checked bool) { a.updateConfig(func(c *config.Config) { c.NoiseGateEnabled = checked }) })
	gateCheck.SetChecked(cfg.NoiseGateEnabled)
	highPassCheck := widget.NewCheck("Pasa-altos", func(checked bool) { a.updateConfig(func(c *config.Config) { c.HighPassEnabled = checked }) })
	highPassCheck.SetChecked(cfg.HighPassEnabled)
	lowPassCheck := widget.NewCheck("Pasa-bajos", func(checked bool) { a.updateConfig(func(c *config.Config) { c.LowPassEnabled = checked }) })
	lowPassCheck.SetChecked(cfg.LowPassEnabled)
	notchCheck := widget.NewCheck("Notch de zumbido", func(checked bool) { a.updateConfig(func(c *config.Config) { c.NotchEnabled = checked }) })
	notchCheck.SetChecked(cfg.NotchEnabled)
	compressorCheck := widget.NewCheck("Compresor", func(checked bool) { a.updateConfig(func(c *config.Config) { c.CompressorEnabled = checked }) })
	compressorCheck.SetChecked(cfg.CompressorEnabled)
	expanderCheck := widget.NewCheck("Expansor", func(checked bool) { a.updateConfig(func(c *config.Config) { c.ExpanderEnabled = checked }) })
	expanderCheck.SetChecked(cfg.ExpanderEnabled)
	limiterCheck := widget.NewCheck("Limitador", func(checked bool) { a.updateConfig(func(c *config.Config) { c.LimiterEnabled = checked }) })
	limiterCheck.SetChecked(cfg.LimiterEnabled)
	newSlider := func(label string, minimum, maximum, value float64, onChanged func(float64)) (*widget.Slider, *widget.Label) {
		valueLabel := widget.NewLabel("")
		slider := widget.NewSlider(minimum, maximum)
		slider.Step = 0.1
		slider.Value = value
		slider.OnChanged = func(current float64) {
			onChanged(current)
			valueLabel.SetText(fmt.Sprintf("%s: %.1f", label, current))
		}
		valueLabel.SetText(fmt.Sprintf("%s: %.1f", label, value))
		return slider, valueLabel
	}
	gain, gainLabel := newSlider("Ganancia (dB)", -24, 24, float64(cfg.InputGainDB), func(value float64) { a.updateConfig(func(c *config.Config) { c.InputGainDB = float32(value) }) })
	highPass, highPassLabel := newSlider("Frecuencia pasa-altos (Hz)", 20, 500, float64(cfg.HighPassHz), func(value float64) { a.updateConfig(func(c *config.Config) { c.HighPassHz = float32(value) }) })
	lowPass, lowPassLabel := newSlider("Frecuencia pasa-bajos (Hz)", 1000, 18000, float64(cfg.LowPassHz), func(value float64) { a.updateConfig(func(c *config.Config) { c.LowPassHz = float32(value) }) })
	notch, notchLabel := newSlider("Frecuencia notch (Hz)", 40, 1000, float64(cfg.NotchHz), func(value float64) { a.updateConfig(func(c *config.Config) { c.NotchHz = float32(value) }) })
	compressorThreshold, compressorThresholdLabel := newSlider("Umbral compresor (dB)", -50, 0, float64(cfg.CompressorThresholdDB), func(value float64) {
		a.updateConfig(func(c *config.Config) { c.CompressorThresholdDB = float32(value) })
	})
	compressorRatio, compressorRatioLabel := newSlider("Ratio compresor", 1, 12, float64(cfg.CompressorRatio), func(value float64) { a.updateConfig(func(c *config.Config) { c.CompressorRatio = float32(value) }) })
	expanderThreshold, expanderThresholdLabel := newSlider("Umbral expansor (dB)", -70, -10, float64(cfg.ExpanderThresholdDB), func(value float64) { a.updateConfig(func(c *config.Config) { c.ExpanderThresholdDB = float32(value) }) })
	expanderRatio, expanderRatioLabel := newSlider("Ratio expansor", 1, 12, float64(cfg.ExpanderRatio), func(value float64) { a.updateConfig(func(c *config.Config) { c.ExpanderRatio = float32(value) }) })
	limiterThreshold, limiterThresholdLabel := newSlider("Techo limitador (dB)", -12, 0, float64(cfg.LimiterThresholdDB), func(value float64) { a.updateConfig(func(c *config.Config) { c.LimiterThresholdDB = float32(value) }) })
	gateThreshold, gateThresholdLabel := newSlider("Umbral de puerta (dBFS)", -60, -5, float64(cfg.ThresholdDB), func(value float64) { a.updateConfig(func(c *config.Config) { c.ThresholdDB = float32(value) }) })
	vadHold, vadHoldLabel := newSlider("Mantener VAD abierto (ms)", 0, 500, float64(cfg.VADHoldMS), func(value float64) { a.updateConfig(func(c *config.Config) { c.VADHoldMS = int(value) }) })
	gateHold, gateHoldLabel := newSlider("Mantener puerta abierta (ms)", 0, 500, float64(cfg.GateHoldMS), func(value float64) { a.updateConfig(func(c *config.Config) { c.GateHoldMS = int(value) }) })
	filterCard := func(title, detail string, controls ...fyne.CanvasObject) fyne.CanvasObject {
		titleLabel := widget.NewLabelWithStyle(title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
		detailLabel := canvas.NewText(detail, color.NRGBA{R: 145, G: 155, B: 165, A: 255})
		detailLabel.TextSize = theme.TextSize() - 2
		content := container.NewVBox(append([]fyne.CanvasObject{titleLabel, detailLabel}, controls...)...)
		background := canvas.NewRectangle(color.NRGBA{R: 32, G: 36, B: 43, A: 255})
		background.CornerRadius = 6
		return container.NewStack(background, container.NewPadded(content))
	}
	advancedFilters := container.NewVBox(
		filterCard("Supresión de ruido", "Reduce ruido constante y de fondo antes de transmitir.", rnnoiseCheck),
		filterCard("Detección de voz (VAD)", "Evita enviar cuando no detecta voz.", vadCheck, thresholdLabel, sensitivity, vadHoldLabel, vadHold),
		filterCard("Puerta de ruido", "Silencia señales por debajo del umbral elegido y evita cortes bruscos.", gateCheck, gateThresholdLabel, gateThreshold, gateHoldLabel, gateHold),
		filterCard("Ganancia", "Ajusta el volumen de entrada antes de los demás filtros.", gainLabel, gain),
		filterCard("Pasa-altos", "Quita golpes, vibración y graves no deseados.", highPassCheck, highPassLabel, highPass),
		filterCard("Pasa-bajos", "Reduce hiss y agudos excesivos.", lowPassCheck, lowPassLabel, lowPass),
		filterCard("Notch de zumbido", "Atenúa una frecuencia puntual de zumbido.", notchCheck, notchLabel, notch),
		filterCard("Compresor", "Acerca los sonidos fuertes y suaves.", compressorCheck, compressorThresholdLabel, compressorThreshold, compressorRatioLabel, compressorRatio),
		filterCard("Expansor", "Baja el ruido de fondo de forma gradual.", expanderCheck, expanderThresholdLabel, expanderThreshold, expanderRatioLabel, expanderRatio),
		filterCard("Limitador", "Evita saturación al hablar muy fuerte.", limiterCheck, limiterThresholdLabel, limiterThreshold),
	)
	advancedFilters.Hide()
	advancedButton := widget.NewButton("Configuración avanzada", func() {
		if advancedFilters.Visible() {
			advancedFilters.Hide()
		} else {
			advancedFilters.Show()
		}
	})
	resetFilters := widget.NewButton("↺", func() {
		defaults := config.Default()
		cfg := a.updateConfig(func(c *config.Config) {
			c.AudioFilterEnabled = defaults.AudioFilterEnabled
			c.RNNoiseEnabled = defaults.RNNoiseEnabled
			c.VADEnabled = defaults.VADEnabled
			c.NoiseGateEnabled = defaults.NoiseGateEnabled
			c.ThresholdDB = defaults.ThresholdDB
			c.VADThreshold = defaults.VADThreshold
			c.InputGainDB = defaults.InputGainDB
			c.HighPassEnabled, c.HighPassHz = defaults.HighPassEnabled, defaults.HighPassHz
			c.LowPassEnabled, c.LowPassHz = defaults.LowPassEnabled, defaults.LowPassHz
			c.NotchEnabled, c.NotchHz = defaults.NotchEnabled, defaults.NotchHz
			c.CompressorEnabled, c.CompressorThresholdDB, c.CompressorRatio = defaults.CompressorEnabled, defaults.CompressorThresholdDB, defaults.CompressorRatio
			c.ExpanderEnabled, c.ExpanderThresholdDB, c.ExpanderRatio = defaults.ExpanderEnabled, defaults.ExpanderThresholdDB, defaults.ExpanderRatio
			c.LimiterEnabled, c.LimiterThresholdDB = defaults.LimiterEnabled, defaults.LimiterThresholdDB
			c.VADHoldMS, c.GateHoldMS = defaults.VADHoldMS, defaults.GateHoldMS
		})
		filterCheck.SetChecked(cfg.AudioFilterEnabled)
		rnnoiseCheck.SetChecked(cfg.RNNoiseEnabled)
		vadCheck.SetChecked(cfg.VADEnabled)
		gateCheck.SetChecked(cfg.NoiseGateEnabled)
		highPassCheck.SetChecked(cfg.HighPassEnabled)
		lowPassCheck.SetChecked(cfg.LowPassEnabled)
		notchCheck.SetChecked(cfg.NotchEnabled)
		compressorCheck.SetChecked(cfg.CompressorEnabled)
		expanderCheck.SetChecked(cfg.ExpanderEnabled)
		limiterCheck.SetChecked(cfg.LimiterEnabled)
		gain.SetValue(float64(cfg.InputGainDB))
		highPass.SetValue(float64(cfg.HighPassHz))
		lowPass.SetValue(float64(cfg.LowPassHz))
		notch.SetValue(float64(cfg.NotchHz))
		compressorThreshold.SetValue(float64(cfg.CompressorThresholdDB))
		compressorRatio.SetValue(float64(cfg.CompressorRatio))
		expanderThreshold.SetValue(float64(cfg.ExpanderThresholdDB))
		expanderRatio.SetValue(float64(cfg.ExpanderRatio))
		limiterThreshold.SetValue(float64(cfg.LimiterThresholdDB))
		gateThreshold.SetValue(float64(cfg.ThresholdDB))
		sensitivity.SetValue(float64(cfg.VADThreshold))
		vadHold.SetValue(float64(cfg.VADHoldMS))
		gateHold.SetValue(float64(cfg.GateHoldMS))
		a.setStatus("Filtros restaurados a valores de fábrica.")
	})
	liveMonitoring := widget.NewCheck("Escuchar mi voz en vivo (en la sala)", func(enabled bool) { a.updateConfig(func(c *config.Config) { c.LiveMonitoring = enabled }) })
	liveMonitoring.SetChecked(cfg.LiveMonitoring)
	phoneBufferCheck := widget.NewCheck("Activar buffer", func(enabled bool) { a.updateConfig(func(c *config.Config) { c.PhoneMicBufferEnabled = enabled }) })
	phoneBufferCheck.SetChecked(cfg.PhoneMicBufferEnabled)
	phoneBufferMS := widget.NewEntry()
	phoneBufferMS.SetText(strconv.Itoa(cfg.PhoneMicBufferMS))
	level := newLevelBar()
	level.SetValue(0)
	levelText := widget.NewLabel("Nivel: -60 dBFS")
	monitor := &levelMonitor{}
	var lastLevelPaint atomic.Int64
	// malgo llama a onLevel por cada buffer de captura (~100 veces/s). Un fyne.Do por llamada
	// saturaba la cola del hilo de UI y cada SetText re-layouteaba la vista: se limita a ~15 fps.
	onLevel := func(db float32) {
		now := time.Now().UnixNano()
		last := lastLevelPaint.Load()
		if now-last < int64(levelPaintInterval) || !lastLevelPaint.CompareAndSwap(last, now) {
			return
		}
		fyne.Do(func() {
			level.SetValue(float64((db + 60) / 60))
			levelText.SetText(fmt.Sprintf("Nivel: %.0f dBFS", db))
		})
	}
	var previewButton *widget.Button
	previewActive := false
	settingsClosed := false
	// Las closures de abajo corren en el hilo de UI; abrir/cerrar dispositivos va en levelMonitor.
	startMonitor := func(name string) {
		if a.connected() {
			monitor.Stop()
			level.SetValue(0)
			levelText.SetText("Nivel: micrófono activo en la sala")
			return
		}
		if name == phoneMicInput {
			monitor.Stop()
			level.SetValue(0)
			levelText.SetText("Nivel: controlado desde el celular")
			return
		}
		monitor.Start(name, onLevel)
	}
	startMonitor(input.Selected)
	stopPreview := func() {
		if !previewActive {
			return
		}
		previewActive = false
		stop := a.detachPreviewAudio()
		go stop()
		if previewButton != nil {
			previewButton.SetText("Escuchar micrófono")
		}
		startMonitor(input.Selected)
	}
	a.settingsStatus = widget.NewLabel("")
	phoneStatusStop := make(chan struct{})
	var closeOnce sync.Once
	var recordingTest atomic.Bool
	var refreshPhoneStatus func()
	lastPhoneState := "" // último estado pintado por refreshPhoneStatus (solo hilo de UI)
	input.OnChanged = func(selected string) {
		stopPreview()
		a.updateConfig(func(c *config.Config) { c.InputDevice = selected })
		startMonitor(selected)
		if selected == phoneMicInput {
			a.showPhoneMic()
		} else {
			go a.stopPhoneMic()
		}
		refreshPhoneStatus()
	}
	output.OnChanged = func(selected string) {
		stopPreview()
		a.updateConfig(func(c *config.Config) { c.OutputDevice = selected })
	}
	testInput := widget.NewButton("Grabar prueba", nil)
	playRecording := widget.NewButton("Reproducir prueba", func() { a.playRecording(output.Selected) })
	testOutput := widget.NewButton("Reproducir tono", func() { a.testOutput(output.Selected) })
	showQR := widget.NewButton("Mostrar QR", func() {
		a.showPhoneMic()
		refreshPhoneStatus()
	})
	stopPhone := widget.NewButton("Detener celular", func() {
		// server.Stop() cierra el servidor HTTP/UDP y espera conexiones: fuera del hilo de UI.
		go func() {
			a.phoneMicMu.RLock()
			server := a.phoneMic
			a.phoneMicMu.RUnlock()
			if server != nil {
				a.stopPhoneMic()
				a.setStatus("Servidor de micrófono de celular detenido.")
			}
			refreshPhoneStatus()
		}()
	})
	bufferSave := widget.NewButton("✓", nil)
	serverLED := canvas.NewCircle(color.NRGBA{R: 210, G: 85, B: 85, A: 255})
	connectionLED := canvas.NewCircle(color.NRGBA{R: 210, G: 85, B: 85, A: 255})
	serverLabel := widget.NewLabel("Servidor detenido")
	connectionLabel := widget.NewLabel("Celular no conectado")
	previewButton = widget.NewButton("Escuchar micrófono", func() {
		if previewActive {
			stopPreview()
			return
		}
		if a.connected() {
			a.setStatus("La prueba de micrófono solo está disponible fuera de una sala.")
			return
		}
		inputName, outputName := input.Selected, output.Selected
		if inputName == phoneMicInput && !a.phoneMicEnabled() {
			a.setStatus("Primero iniciá el servidor y conectá el celular.")
			return
		}
		monitor.Stop()
		previewButton.Disable()
		go func() {
			err := a.startMicPreview(inputName, outputName)
			fyne.Do(func() {
				previewButton.Enable()
				if settingsClosed {
					stop := a.detachPreviewAudio()
					go stop()
					return
				}
				if err != nil {
					a.setStatus("No se pudo iniciar la escucha: " + err.Error())
					startMonitor(input.Selected)
					return
				}
				previewActive = true
				previewButton.SetText("Dejar de escuchar")
				a.setStatus("Escuchando el micrófono. Ajustá filtro y VAD para probar.")
			})
		}()
	})
	applyBuffer := func() bool {
		bufferMS, parseErr := strconv.Atoi(strings.TrimSpace(phoneBufferMS.Text))
		if parseErr != nil || bufferMS < 40 || bufferMS > 1000 {
			a.setStatus("El buffer del celular debe estar entre 40 y 1000 ms.")
			return false
		}
		a.updateConfig(func(c *config.Config) { c.PhoneMicBufferMS = bufferMS })
		return true
	}
	bufferSave.OnTapped = func() {
		if !applyBuffer() {
			return
		}
		a.setStatus("Buffer aplicado. Reiniciá el servidor del celular para usarlo.")
	}
	testInput.OnTapped = func() {
		if a.connected() {
			a.setStatus("No se puede grabar prueba mientras estás en una sala.")
			return
		}
		if !recordingTest.CompareAndSwap(false, true) {
			return
		}
		testInput.Disable()
		lastPhoneState = "" // el Disable manual desincroniza el caché: forzar el próximo repintado
		a.testInput(input.Selected)
		duration := 2200 * time.Millisecond
		if input.Selected == phoneMicInput {
			duration = 5 * time.Second
		}
		time.AfterFunc(duration, func() {
			recordingTest.Store(false)
			refreshPhoneStatus()
		})
	}
	refreshPhoneStatus = func() {
		a.phoneMicMu.RLock()
		server := a.phoneMic
		a.phoneMicMu.RUnlock()
		listening := server != nil
		connected := listening && server.Connected()
		recording := recordingTest.Load()
		fyne.Do(func() {
			// input.Selected es estado del widget: se lee en el hilo de UI. El ticker llama cada
			// 500 ms; si nada cambió no se tocan widgets (cada Refresh/Enable fuerza un repintado).
			phoneSelected := input.Selected == phoneMicInput
			state := fmt.Sprint(phoneSelected, listening, connected, recording)
			if state == lastPhoneState {
				return
			}
			lastPhoneState = state
			serverLED.FillColor = color.NRGBA{R: 210, G: 85, B: 85, A: 255}
			serverLabel.SetText("Servidor detenido")
			connectionLED.FillColor = color.NRGBA{R: 210, G: 85, B: 85, A: 255}
			connectionLabel.SetText("Celular no conectado")
			if listening {
				serverLED.FillColor = color.NRGBA{R: 90, G: 205, B: 120, A: 255}
				serverLabel.SetText("Servidor escuchando")
			}
			if connected {
				connectionLED.FillColor = color.NRGBA{R: 90, G: 205, B: 120, A: 255}
				connectionLabel.SetText("Celular conectado")
			}
			serverLED.Refresh()
			connectionLED.Refresh()
			if phoneSelected {
				phoneBufferCheck.Enable()
				phoneBufferMS.Enable()
				bufferSave.Enable()
				showQR.Enable()
				if listening {
					stopPhone.Enable()
				} else {
					stopPhone.Disable()
				}
				if !connected || recording {
					testInput.Disable()
				} else {
					testInput.Enable()
				}
			} else {
				phoneBufferCheck.Disable()
				phoneBufferMS.Disable()
				bufferSave.Disable()
				showQR.Disable()
				stopPhone.Disable()
				if recording {
					testInput.Disable()
				} else {
					testInput.Enable()
				}
			}
		})
	}
	refreshPhoneStatus()
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-phoneStatusStop:
				return
			case <-ticker.C:
				refreshPhoneStatus()
			}
		}
	}()
	closeSettings := func() {
		closeOnce.Do(func() {
			settingsClosed = true
			close(phoneStatusStop)
			// Antes stopPreview corría en una goroutine: tocaba previewButton fuera del hilo de UI
			// y volvía a abrir el medidor de nivel justo al salir de ajustes.
			monitor.Stop()
			previewActive = false
			stop := a.detachPreviewAudio()
			go stop()
			a.cfgBaseline.Store(nil)
			a.settingsStatus = nil
			a.window.SetContent(a.mainView)
		})
	}
	roomPortEntry := widget.NewEntry()
	cleanPort := strings.TrimPrefix(cfg.ListenAddress, ":")
	if cleanPort == "" || cleanPort == "0" {
		cleanPort = "47830"
	}
	roomPortEntry.SetText(cleanPort)

	phonePortEntry := widget.NewEntry()
	phonePortVal := cfg.PhoneMicPort
	if phonePortVal <= 0 {
		phonePortVal = 47831
	}
	phonePortEntry.SetText(strconv.Itoa(phonePortVal))
	openedTexts := []string{username.Text, roomPortEntry.Text, phonePortEntry.Text, phoneBufferMS.Text}
	isDirty := func() bool {
		if a.conf().LiveSettings != baseline {
			return true
		}
		for i, entry := range []*widget.Entry{username, roomPortEntry, phonePortEntry, phoneBufferMS} {
			if strings.TrimSpace(entry.Text) != strings.TrimSpace(openedTexts[i]) {
				return true
			}
		}
		return false
	}

	saveSettings := func() {
		if !applyBuffer() {
			return
		}
		name := strings.TrimSpace(username.Text)
		if name == "" {
			name = "Usuario"
		}
		listenAddress := ":47830"
		if portStr := strings.TrimSpace(roomPortEntry.Text); portStr != "" {
			if !strings.Contains(portStr, ":") {
				listenAddress = ":" + portStr
			} else {
				listenAddress = portStr
			}
		}
		phonePort, phonePortErr := strconv.Atoi(strings.TrimSpace(phonePortEntry.Text))
		cfg := a.updateConfig(func(c *config.Config) {
			c.Username = name
			c.ListenAddress = listenAddress
			if phonePortErr == nil && phonePort > 0 && phonePort < 65536 {
				c.PhoneMicPort = phonePort
			}
		})
		if a.selfNameLabel != nil {
			a.selfNameLabel.SetText(cfg.Username)
		}
		for i := range a.roomState.Participants {
			if a.roomState.Participants[i].ID == a.participantID {
				a.roomState.Participants[i].CanBeHost = cfg.AllowHostMigration
				break
			}
		}
		if a.bottomBar != nil {
			if cfg.ShowStatusBar {
				a.bottomBar.Show()
			} else {
				a.bottomBar.Hide()
			}
		}
		if engine := a.engine(); engine != nil {
			engine.SetJitterBufferMS(cfg.JitterBufferMS)
		}
		a.setStatus("Configuración guardada.")
		closeSettings()
		a.saveConfig()
		// Los selects publican el dispositivo en vivo: se compara contra el que había al abrir
		// ajustes (antes se comparaba contra a.cfg ya modificado y nunca reiniciaba).
		if a.connected() && (openedInput != cfg.InputDevice || openedOutput != cfg.OutputDevice) {
			go a.restartAudioInRoom()
		}
	}
	// discardSettings vuelve LiveSettings a la base (CAS: no pisa LastPeer, token, etc. que hayan
	// cambiado en el medio) y deshace los efectos que los callbacks aplicaron fuera de la config.
	// El dispositivo de la sala no se toca en vivo (solo Guardar reinicia el audio), así que no hay
	// nada que reabrir.
	discardSettings := func() {
		a.updateConfig(func(c *config.Config) { c.LiveSettings = baseline })
		logging.SetLevel(baseline.LogLevel)
		if engine := a.engine(); engine != nil {
			engine.SetJitterBufferMS(baseline.JitterBufferMS)
		}
		a.phoneMicMu.RLock()
		phoneRunning := a.phoneMic != nil
		a.phoneMicMu.RUnlock()
		switch {
		case phoneRunning && !phoneWasRunning:
			go a.stopPhoneMic()
		case !phoneRunning && phoneWasRunning:
			go func() {
				if _, err := a.ensurePhoneMicServer(); err != nil {
					a.setStatus("No se pudo reiniciar el servidor del celular: " + err.Error())
				}
			}()
		}
		a.setStatus("Cambios descartados.")
		closeSettings()
	}
	askBack := func() {
		if !isDirty() {
			closeSettings()
			return
		}
		var ask *dialog.CustomDialog
		saveButton := widget.NewButton("Guardar", func() {
			ask.Hide()
			saveSettings()
		})
		saveButton.Importance = widget.HighImportance
		ask = dialog.NewCustomWithoutButtons("Cambios sin guardar", widget.NewLabel("¿Querés guardar los cambios antes de salir?"), a.window)
		ask.SetButtons([]fyne.CanvasObject{
			widget.NewButton("Seguir editando", func() { ask.Hide() }),
			widget.NewButton("Descartar", func() {
				ask.Hide()
				discardSettings()
			}),
			saveButton,
		})
		ask.Show()
	}
	askCancel := func() {
		if !isDirty() {
			closeSettings()
			return
		}
		ask := dialog.NewConfirm("Descartar cambios", "Se van a revertir los cambios que hiciste en la configuración. ¿Continuar?", func(confirmed bool) {
			if confirmed {
				discardSettings()
			}
		}, a.window)
		ask.SetConfirmText("Descartar")
		ask.SetDismissText("Seguir editando")
		ask.Show()
	}
	outputFilterCheck := widget.NewCheck("Aplicar filtro de salida", func(enabled bool) { a.updateConfig(func(c *config.Config) { c.OutputFilterEnabled = enabled }) })
	outputFilterCheck.SetChecked(cfg.OutputFilterEnabled)
	outputHighPassCheck := widget.NewCheck("Pasa-altos", func(enabled bool) { a.updateConfig(func(c *config.Config) { c.OutputHighPassEnabled = enabled }) })
	outputHighPassCheck.SetChecked(cfg.OutputHighPassEnabled)
	outputLowPassCheck := widget.NewCheck("Pasa-bajos", func(enabled bool) { a.updateConfig(func(c *config.Config) { c.OutputLowPassEnabled = enabled }) })
	outputLowPassCheck.SetChecked(cfg.OutputLowPassEnabled)
	outputLimiterCheck := widget.NewCheck("Limitador", func(enabled bool) { a.updateConfig(func(c *config.Config) { c.OutputLimiterEnabled = enabled }) })
	outputLimiterCheck.SetChecked(cfg.OutputLimiterEnabled)
	outputGain, outputGainLabel := newSlider("Ganancia salida (dB)", -24, 24, float64(cfg.OutputGainDB), func(value float64) { a.updateConfig(func(c *config.Config) { c.OutputGainDB = float32(value) }) })
	outputHighPass, outputHighPassLabel := newSlider("Frecuencia pasa-altos (Hz)", 20, 500, float64(cfg.OutputHighPassHz), func(value float64) { a.updateConfig(func(c *config.Config) { c.OutputHighPassHz = float32(value) }) })
	outputLowPass, outputLowPassLabel := newSlider("Frecuencia pasa-bajos (Hz)", 1000, 18000, float64(cfg.OutputLowPassHz), func(value float64) { a.updateConfig(func(c *config.Config) { c.OutputLowPassHz = float32(value) }) })
	outputLimiter, outputLimiterLabel := newSlider("Techo limitador (dB)", -12, 0, float64(cfg.OutputLimiterDB), func(value float64) { a.updateConfig(func(c *config.Config) { c.OutputLimiterDB = float32(value) }) })
	outputAdvanced := container.NewVBox(
		filterCard("Ganancia", "Ajusta el volumen de las voces recibidas.", outputGainLabel, outputGain),
		filterCard("Pasa-altos", "Reduce graves, golpes y vibración en las voces recibidas.", outputHighPassCheck, outputHighPassLabel, outputHighPass),
		filterCard("Pasa-bajos", "Reduce hiss y agudos molestos en las voces recibidas.", outputLowPassCheck, outputLowPassLabel, outputLowPass),
		filterCard("Limitador", "Evita saturación y picos molestos en la salida.", outputLimiterCheck, outputLimiterLabel, outputLimiter),
	)
	outputAdvanced.Hide()
	outputAdvancedButton := widget.NewButton("Configuración avanzada", func() {
		if outputAdvanced.Visible() {
			outputAdvanced.Hide()
		} else {
			outputAdvanced.Show()
		}
	})
	resetOutputFilters := widget.NewButton("↺", func() {
		defaults := config.Default()
		cfg := a.updateConfig(func(c *config.Config) {
			c.OutputFilterEnabled, c.OutputGainDB = defaults.OutputFilterEnabled, defaults.OutputGainDB
			c.OutputHighPassEnabled, c.OutputHighPassHz = defaults.OutputHighPassEnabled, defaults.OutputHighPassHz
			c.OutputLowPassEnabled, c.OutputLowPassHz = defaults.OutputLowPassEnabled, defaults.OutputLowPassHz
			c.OutputLimiterEnabled, c.OutputLimiterDB = defaults.OutputLimiterEnabled, defaults.OutputLimiterDB
		})
		outputFilterCheck.SetChecked(cfg.OutputFilterEnabled)
		outputHighPassCheck.SetChecked(cfg.OutputHighPassEnabled)
		outputLowPassCheck.SetChecked(cfg.OutputLowPassEnabled)
		outputLimiterCheck.SetChecked(cfg.OutputLimiterEnabled)
		outputGain.SetValue(float64(cfg.OutputGainDB))
		outputHighPass.SetValue(float64(cfg.OutputHighPassHz))
		outputLowPass.SetValue(float64(cfg.OutputLowPassHz))
		outputLimiter.SetValue(float64(cfg.OutputLimiterDB))
		a.setStatus("Filtros de salida restaurados a valores de fábrica.")
	})
	showStatusBarCheck := widget.NewCheck("Mostrar barra de estado inferior", func(checked bool) { a.updateConfig(func(c *config.Config) { c.ShowStatusBar = checked }) })
	showStatusBarCheck.SetChecked(cfg.ShowStatusBar)
	profileCard := uiCard(container.NewVBox(
		widget.NewLabelWithStyle("👤 Perfil de Usuario", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabel("Nombre de usuario"),
		username,
	))
	inputCard := uiCard(container.NewVBox(
		widget.NewLabelWithStyle("🎙️ Entrada de Audio", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabel("Fuente de voz"),
		input,
		levelText,
		level,
		mode,
		container.NewHBox(filterCheck, advancedButton, resetFilters),
		advancedFilters,
		liveMonitoring,
		previewButton,
		testInput,
	))
	outputCard := uiCard(container.NewVBox(
		widget.NewLabelWithStyle("🔊 Salida de Audio", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabel("Dispositivo de reproducción"),
		output,
		container.NewGridWithColumns(2, testOutput, playRecording),
		container.NewHBox(outputFilterCheck, outputAdvancedButton, resetOutputFilters),
		outputAdvanced,
	))
	phoneCard := uiCard(container.NewVBox(
		widget.NewLabelWithStyle("📱 Micrófono del Celular", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		container.NewBorder(nil, nil, container.NewGridWrap(fyne.NewSize(12, 12), serverLED), nil, serverLabel),
		container.NewBorder(nil, nil, container.NewGridWrap(fyne.NewSize(12, 12), connectionLED), nil, connectionLabel),
		container.NewHBox(phoneBufferCheck, container.NewGridWrap(fyne.NewSize(76, 36), phoneBufferMS), container.NewGridWrap(fyne.NewSize(36, 36), bufferSave)),
		container.NewGridWithColumns(2, showQR, stopPhone),
	))
	eventSoundsCheck := widget.NewCheck("Sonidos de eventos (conexión, desconexión, chat)", func(checked bool) { a.updateConfig(func(c *config.Config) { c.EventSoundsEnabled = checked }) })
	eventSoundsCheck.SetChecked(cfg.EventSoundsEnabled)
	allowHostCheck := widget.NewCheck("Permitir ser Host de respaldo si el Host se desconecta", func(checked bool) { a.updateConfig(func(c *config.Config) { c.AllowHostMigration = checked }) })
	allowHostCheck.SetChecked(cfg.AllowHostMigration)

	jitterLabel := widget.NewLabel(fmt.Sprintf(jitterLabelFormat, cfg.JitterBufferMS))
	jitterHint := widget.NewLabel("El colchón real se ajusta solo según el jitter medido de cada participante; esto es el máximo.")
	jitterHint.Wrapping = fyne.TextWrapWord
	jitterSlider := widget.NewSlider(40, 500)
	jitterSlider.Step = 10
	jitterSlider.Value = float64(cfg.JitterBufferMS)
	jitterSlider.OnChanged = func(val float64) {
		a.updateConfig(func(c *config.Config) { c.JitterBufferMS = int(val) })
		jitterLabel.SetText(fmt.Sprintf(jitterLabelFormat, int(val)))
		if engine := a.engine(); engine != nil {
			engine.SetJitterBufferMS(int(val))
		}
	}

	networkCard := uiCard(container.NewVBox(
		widget.NewLabelWithStyle("🌐 Red y Conectividad", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabel("Puerto UDP de la sala (predeterminado 47830):"),
		roomPortEntry,
		widget.NewLabel("Puerto UDP para el celular (predeterminado 47831):"),
		phonePortEntry,
		allowHostCheck,
		jitterLabel,
		jitterSlider,
		jitterHint,
	))
	openDataDirButton := widget.NewButtonWithIcon("Abrir carpeta de datos y grabaciones", theme.FolderOpenIcon(), func() {
		if err := openDataDirectory(); err != nil {
			a.setStatus("No se pudo abrir la carpeta: " + err.Error())
		}
	})
	openLogsDirButton := widget.NewButtonWithIcon("Abrir carpeta de registros (logs)", theme.DocumentIcon(), func() {
		if err := openLogsDirectory(); err != nil {
			a.setStatus("No se pudo abrir la carpeta de logs: " + err.Error())
		}
	})
	checkUpdatesButton := widget.NewButtonWithIcon("Buscar actualizaciones", theme.ViewRefreshIcon(), func() {
		go a.checkForUpdates(true)
	})
	logLevelLabel := widget.NewLabel("Nivel de registros (logs):")
	logLevelOptions := []string{
		"0 - Solo Errores",
		"1 - Debug Normal",
		"2 - Debug Avanzado",
	}
	logLevelSelect := widget.NewSelect(logLevelOptions, func(selected string) {
		level := logging.LevelError
		switch selected {
		case "1 - Debug Normal":
			level = logging.LevelControl
		case "2 - Debug Avanzado":
			level = logging.LevelTrace
		}
		a.updateConfig(func(c *config.Config) { c.LogLevel = level })
		logging.SetLevel(level)
	})
	switch cfg.LogLevel {
	case logging.LevelControl:
		logLevelSelect.SetSelected("1 - Debug Normal")
	case logging.LevelTrace:
		logLevelSelect.SetSelected("2 - Debug Avanzado")
	default:
		logLevelSelect.SetSelected("0 - Solo Errores")
	}
	interfaceCard := uiCard(container.NewVBox(
		widget.NewLabelWithStyle("⚙️ Interfaz y Sistema", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		showStatusBarCheck,
		eventSoundsCheck,
		logLevelLabel,
		logLevelSelect,
		container.NewGridWithColumns(3, openDataDirButton, openLogsDirButton, checkUpdatesButton),
	))
	settingsBody := container.NewVScroll(container.NewVBox(
		profileCard,
		container.NewGridWithColumns(2, inputCard, outputCard),
		phoneCard,
		networkCard,
		interfaceCard,
	))
	back := widget.NewButtonWithIcon("Volver", theme.NavigateBackIcon(), askBack)
	header := container.NewBorder(nil, nil, back, nil, widget.NewLabelWithStyle("Configuración", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))
	footer := container.NewBorder(nil, nil, a.settingsStatus, container.NewHBox(widget.NewButton("Cancelar", askCancel), widget.NewButton("Guardar", saveSettings)), nil)
	a.window.SetContent(container.NewBorder(header, footer, nil, nil, settingsBody))
}

// showPhoneMic arranca el servidor del celular (si hace falta) y muestra el QR. Arrancar el
// servidor abre sockets, puede generar el certificado TLS y guarda la config: va en segundo plano
// y solo el diálogo se arma en el hilo de UI.
func (a *App) showPhoneMic() {
	a.updateConfig(func(c *config.Config) { c.InputDevice = phoneMicInput })
	go func() {
		server, err := a.ensurePhoneMicServer()
		if err != nil {
			a.setStatus("No se pudo iniciar el micrófono de celular: " + err.Error())
			return
		}
		png, err := server.QRCode()
		if err != nil {
			a.setStatus("No se pudo crear el QR: " + err.Error())
			return
		}
		serverURL := server.URL()
		fyne.Do(func() {
			qr := canvas.NewImageFromResource(fyne.NewStaticResource("voxmesh-phone-mic.png", png))
			qr.FillMode = canvas.ImageFillContain
			qr.Resize(fyne.NewSize(280, 280))
			address := widget.NewMultiLineEntry()
			address.SetText(serverURL)
			address.Disable()
			content := container.NewVBox(
				widget.NewLabel("Escaneá el código QR desde la app VoxMesh Mic (misma Wi-Fi):"),
				container.NewCenter(container.NewGridWrap(fyne.NewSize(280, 280), qr)),
				address,
			)
			dialog.ShowCustom("Usar celular como micrófono", "Cerrar", content, a.window)
		})
	}()
}

// levelPaintInterval limita los repintados del medidor de nivel (~15 fps).
const levelPaintInterval = 66 * time.Millisecond

// levelMonitor abre y cierra el medidor de nivel de ajustes fuera del hilo de UI:
// malgo.InitContext/InitDevice (WASAPI) tarda cientos de ms, o segundos con dispositivos
// Bluetooth. gen descarta un arranque viejo (se cambió de dispositivo o se cerró la vista
// mientras el anterior todavía inicializaba).
type levelMonitor struct {
	mu      sync.Mutex
	gen     uint64
	current *audio.Monitor
}

func (m *levelMonitor) detach() (*audio.Monitor, uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gen++
	old := m.current
	m.current = nil
	return old, m.gen
}

func (m *levelMonitor) Start(name string, onLevel func(float32)) {
	old, gen := m.detach()
	go func() {
		old.Stop()
		started, err := audio.StartMonitor(name, onLevel)
		if err != nil {
			logging.Errorf("medidor de nivel %q: %s", name, logging.FormatError(err))
			return
		}
		m.mu.Lock()
		stale := m.gen != gen
		if !stale {
			m.current = started
		}
		m.mu.Unlock()
		if stale {
			started.Stop()
		}
	}()
}

func (m *levelMonitor) Stop() {
	if old, _ := m.detach(); old != nil {
		go old.Stop()
	}
}

func (a *App) testPhoneMic() {
	a.phoneMicMu.RLock()
	server := a.phoneMic
	a.phoneMicMu.RUnlock()
	if server == nil {
		a.setStatus("Primero abrí 'Usar tu celular como micrófono' y conectá el teléfono.")
		return
	}
	if !server.Connected() {
		a.setStatus("Esperando que el celular se conecte para grabar.")
		return
	}
	server.ResetRecording()
	a.setStatus("Grabando desde el celular durante 5 segundos...")
	go func() {
		time.Sleep(5 * time.Second)
		data := server.Recording()
		if len(data) == 0 {
			a.setStatus("No llegó audio desde el celular.")
			return
		}
		a.recordingMu.Lock()
		a.recordedInput = data
		a.recordingMu.Unlock()
		a.setStatus("Grabación del celular lista. Usá 'Reproducir grabación'.")
	}()
}

// jitterLabelFormat es el texto del slider de JitterBufferMS: desde que el colchón es
// adaptativo, el slider fija su máximo.
const jitterLabelFormat = "Buffer de recepción máximo (adaptativo): %d ms"

func (a *App) testInput(name string) {
	if name == phoneMicInput {
		a.testPhoneMic()
		return
	}
	a.setStatus("Grabando desde " + name + "...")
	cfg := a.conf()
	filterEnabled := cfg.AudioFilterEnabled
	thresholdDB := cfg.ThresholdDB
	go func() {
		data, err := audio.RecordInput(name, 2*time.Second)
		if err != nil {
			a.setStatus("No se pudo grabar: " + err.Error())
			return
		}
		if filterEnabled {
			data, _, err = audio.PrepareOutgoingPCM(data, a.voiceProcessor, audio.NoiseGate{ThresholdDB: thresholdDB}, true, cfg.VADThreshold)
		} else {
			data, err = audio.PreparePCM(data, audio.NoiseGate{ThresholdDB: thresholdDB}, false)
		}
		if err != nil {
			a.setStatus("No se pudo aplicar el filtro: " + err.Error())
			return
		}
		a.recordingMu.Lock()
		a.recordedInput = append(a.recordedInput[:0], data...)
		a.recordingMu.Unlock()
		mode := "audio crudo"
		if filterEnabled {
			mode = "audio con filtro"
		}
		a.setStatus("Micrófono OK: " + mode + ", " + strconv.Itoa(len(data)) + " bytes.")
	}()
}

func (a *App) testOutput(name string) {
	a.setStatus("Reproduciendo tono en " + name + "...")
	go func() {
		if err := audio.PlayTestTone(name, time.Second); err != nil {
			a.setStatus("No se pudo reproducir: " + err.Error())
			return
		}
		a.setStatus("Salida de audio OK.")
	}()
}

func (a *App) playRecording(name string) {
	a.recordingMu.RLock()
	data := append([]byte(nil), a.recordedInput...)
	a.recordingMu.RUnlock()
	if len(data) == 0 {
		a.setStatus("Primero graba una prueba de micrófono.")
		return
	}
	a.setStatus("Reproduciendo la grabación...")
	go func() {
		if err := audio.PlayPCM(name, data); err != nil {
			a.setStatus("No se pudo reproducir la grabación: " + err.Error())
			return
		}
		a.setStatus("Grabación reproducida.")
	}()
}
