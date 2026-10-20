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
	username := widget.NewEntry()
	username.SetText(a.cfg.Username)
	input := widget.NewSelect(inputOptions, nil)
	input.SetSelected(a.cfg.InputDevice)
	output := widget.NewSelect(outputOptions, nil)
	output.SetSelected(a.cfg.OutputDevice)
	sensitivity := widget.NewSlider(0, 1)
	sensitivity.Step = 0.05
	sensitivity.Value = float64(a.cfg.VADThreshold)
	thresholdLabel := widget.NewLabel(fmt.Sprintf("Sensibilidad VAD: %.2f", a.cfg.VADThreshold))
	sensitivity.OnChanged = func(value float64) {
		a.cfg.VADThreshold = float32(value)
		thresholdLabel.SetText(fmt.Sprintf("Sensibilidad VAD: %.2f", value))
	}
	mode := widget.NewRadioGroup([]string{"Activación por voz", "Push-to-talk"}, func(selected string) {
		if selected == "Push-to-talk" {
			a.cfg.MicMode = config.PushToTalk
		} else {
			a.cfg.MicMode = config.VoiceActivation
		}
	})
	if a.cfg.MicMode == config.PushToTalk {
		mode.SetSelected("Push-to-talk")
	} else {
		mode.SetSelected("Activación por voz")
	}
	filterCheck := widget.NewCheck("Aplicar filtro de voz", func(checked bool) { a.cfg.AudioFilterEnabled = checked })
	filterCheck.SetChecked(a.cfg.AudioFilterEnabled)
	rnnoiseCheck := widget.NewCheck("Supresión de ruido (RNNoise)", func(checked bool) { a.cfg.RNNoiseEnabled = checked })
	rnnoiseCheck.SetChecked(a.cfg.RNNoiseEnabled)
	vadCheck := widget.NewCheck("Detección de voz (VAD)", func(checked bool) { a.cfg.VADEnabled = checked })
	vadCheck.SetChecked(a.cfg.VADEnabled)
	gateCheck := widget.NewCheck("Puerta de ruido", func(checked bool) { a.cfg.NoiseGateEnabled = checked })
	gateCheck.SetChecked(a.cfg.NoiseGateEnabled)
	highPassCheck := widget.NewCheck("Pasa-altos", func(checked bool) { a.cfg.HighPassEnabled = checked })
	highPassCheck.SetChecked(a.cfg.HighPassEnabled)
	lowPassCheck := widget.NewCheck("Pasa-bajos", func(checked bool) { a.cfg.LowPassEnabled = checked })
	lowPassCheck.SetChecked(a.cfg.LowPassEnabled)
	notchCheck := widget.NewCheck("Notch de zumbido", func(checked bool) { a.cfg.NotchEnabled = checked })
	notchCheck.SetChecked(a.cfg.NotchEnabled)
	compressorCheck := widget.NewCheck("Compresor", func(checked bool) { a.cfg.CompressorEnabled = checked })
	compressorCheck.SetChecked(a.cfg.CompressorEnabled)
	expanderCheck := widget.NewCheck("Expansor", func(checked bool) { a.cfg.ExpanderEnabled = checked })
	expanderCheck.SetChecked(a.cfg.ExpanderEnabled)
	limiterCheck := widget.NewCheck("Limitador", func(checked bool) { a.cfg.LimiterEnabled = checked })
	limiterCheck.SetChecked(a.cfg.LimiterEnabled)
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
	gain, gainLabel := newSlider("Ganancia (dB)", -24, 24, float64(a.cfg.InputGainDB), func(value float64) { a.cfg.InputGainDB = float32(value) })
	highPass, highPassLabel := newSlider("Frecuencia pasa-altos (Hz)", 20, 500, float64(a.cfg.HighPassHz), func(value float64) { a.cfg.HighPassHz = float32(value) })
	lowPass, lowPassLabel := newSlider("Frecuencia pasa-bajos (Hz)", 1000, 18000, float64(a.cfg.LowPassHz), func(value float64) { a.cfg.LowPassHz = float32(value) })
	notch, notchLabel := newSlider("Frecuencia notch (Hz)", 40, 1000, float64(a.cfg.NotchHz), func(value float64) { a.cfg.NotchHz = float32(value) })
	compressorThreshold, compressorThresholdLabel := newSlider("Umbral compresor (dB)", -50, 0, float64(a.cfg.CompressorThresholdDB), func(value float64) { a.cfg.CompressorThresholdDB = float32(value) })
	compressorRatio, compressorRatioLabel := newSlider("Ratio compresor", 1, 12, float64(a.cfg.CompressorRatio), func(value float64) { a.cfg.CompressorRatio = float32(value) })
	expanderThreshold, expanderThresholdLabel := newSlider("Umbral expansor (dB)", -70, -10, float64(a.cfg.ExpanderThresholdDB), func(value float64) { a.cfg.ExpanderThresholdDB = float32(value) })
	expanderRatio, expanderRatioLabel := newSlider("Ratio expansor", 1, 12, float64(a.cfg.ExpanderRatio), func(value float64) { a.cfg.ExpanderRatio = float32(value) })
	limiterThreshold, limiterThresholdLabel := newSlider("Techo limitador (dB)", -12, 0, float64(a.cfg.LimiterThresholdDB), func(value float64) { a.cfg.LimiterThresholdDB = float32(value) })
	gateThreshold, gateThresholdLabel := newSlider("Umbral de puerta (dBFS)", -60, -5, float64(a.cfg.ThresholdDB), func(value float64) { a.cfg.ThresholdDB = float32(value) })
	vadHold, vadHoldLabel := newSlider("Mantener VAD abierto (ms)", 0, 500, float64(a.cfg.VADHoldMS), func(value float64) { a.cfg.VADHoldMS = int(value) })
	gateHold, gateHoldLabel := newSlider("Mantener puerta abierta (ms)", 0, 500, float64(a.cfg.GateHoldMS), func(value float64) { a.cfg.GateHoldMS = int(value) })
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
		a.cfg.AudioFilterEnabled = defaults.AudioFilterEnabled
		a.cfg.RNNoiseEnabled = defaults.RNNoiseEnabled
		a.cfg.VADEnabled = defaults.VADEnabled
		a.cfg.NoiseGateEnabled = defaults.NoiseGateEnabled
		a.cfg.ThresholdDB = defaults.ThresholdDB
		a.cfg.VADThreshold = defaults.VADThreshold
		a.cfg.InputGainDB = defaults.InputGainDB
		a.cfg.HighPassEnabled, a.cfg.HighPassHz = defaults.HighPassEnabled, defaults.HighPassHz
		a.cfg.LowPassEnabled, a.cfg.LowPassHz = defaults.LowPassEnabled, defaults.LowPassHz
		a.cfg.NotchEnabled, a.cfg.NotchHz = defaults.NotchEnabled, defaults.NotchHz
		a.cfg.CompressorEnabled, a.cfg.CompressorThresholdDB, a.cfg.CompressorRatio = defaults.CompressorEnabled, defaults.CompressorThresholdDB, defaults.CompressorRatio
		a.cfg.ExpanderEnabled, a.cfg.ExpanderThresholdDB, a.cfg.ExpanderRatio = defaults.ExpanderEnabled, defaults.ExpanderThresholdDB, defaults.ExpanderRatio
		a.cfg.LimiterEnabled, a.cfg.LimiterThresholdDB = defaults.LimiterEnabled, defaults.LimiterThresholdDB
		a.cfg.VADHoldMS, a.cfg.GateHoldMS = defaults.VADHoldMS, defaults.GateHoldMS
		filterCheck.SetChecked(a.cfg.AudioFilterEnabled)
		rnnoiseCheck.SetChecked(a.cfg.RNNoiseEnabled)
		vadCheck.SetChecked(a.cfg.VADEnabled)
		gateCheck.SetChecked(a.cfg.NoiseGateEnabled)
		highPassCheck.SetChecked(a.cfg.HighPassEnabled)
		lowPassCheck.SetChecked(a.cfg.LowPassEnabled)
		notchCheck.SetChecked(a.cfg.NotchEnabled)
		compressorCheck.SetChecked(a.cfg.CompressorEnabled)
		expanderCheck.SetChecked(a.cfg.ExpanderEnabled)
		limiterCheck.SetChecked(a.cfg.LimiterEnabled)
		gain.SetValue(float64(a.cfg.InputGainDB))
		highPass.SetValue(float64(a.cfg.HighPassHz))
		lowPass.SetValue(float64(a.cfg.LowPassHz))
		notch.SetValue(float64(a.cfg.NotchHz))
		compressorThreshold.SetValue(float64(a.cfg.CompressorThresholdDB))
		compressorRatio.SetValue(float64(a.cfg.CompressorRatio))
		expanderThreshold.SetValue(float64(a.cfg.ExpanderThresholdDB))
		expanderRatio.SetValue(float64(a.cfg.ExpanderRatio))
		limiterThreshold.SetValue(float64(a.cfg.LimiterThresholdDB))
		gateThreshold.SetValue(float64(a.cfg.ThresholdDB))
		sensitivity.SetValue(float64(a.cfg.VADThreshold))
		vadHold.SetValue(float64(a.cfg.VADHoldMS))
		gateHold.SetValue(float64(a.cfg.GateHoldMS))
		a.setStatus("Filtros restaurados a valores de fábrica.")
	})
	liveMonitoring := widget.NewCheck("Escuchar mi voz en vivo (en la sala)", func(enabled bool) { a.cfg.LiveMonitoring = enabled })
	liveMonitoring.SetChecked(a.cfg.LiveMonitoring)
	phoneBufferCheck := widget.NewCheck("Activar buffer", func(enabled bool) { a.cfg.PhoneMicBufferEnabled = enabled })
	phoneBufferCheck.SetChecked(a.cfg.PhoneMicBufferEnabled)
	phoneBufferMS := widget.NewEntry()
	phoneBufferMS.SetText(strconv.Itoa(a.cfg.PhoneMicBufferMS))
	level := newLevelBar()
	level.SetValue(0)
	levelText := widget.NewLabel("Nivel: -60 dBFS")
	var monitor *audio.Monitor
	var previewButton *widget.Button
	previewActive := false
	startMonitor := func(name string) {
		if monitor != nil {
			monitor.Stop()
			monitor = nil
		}
		if a.transport != nil {
			fyne.Do(func() {
				level.SetValue(0)
				levelText.SetText("Nivel: micrófono activo en la sala")
			})
			return
		}
		if name == phoneMicInput {
			fyne.Do(func() {
				level.SetValue(0)
				levelText.SetText("Nivel: controlado desde el celular")
			})
			return
		}
		monitor, _ = audio.StartMonitor(name, func(db float32) {
			fyne.Do(func() {
				level.SetValue(float64((db + 60) / 60))
				levelText.SetText(fmt.Sprintf("Nivel: %.0f dBFS", db))
			})
		})
	}
	startMonitor(input.Selected)
	stopPreview := func() {
		if !previewActive {
			return
		}
		a.stopPreviewAudio()
		previewActive = false
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
	input.OnChanged = func(selected string) {
		stopPreview()
		a.cfg.InputDevice = selected
		startMonitor(selected)
		if selected == phoneMicInput {
			a.showPhoneMic()
		} else {
			a.stopPhoneMic()
		}
		refreshPhoneStatus()
	}
	output.OnChanged = func(selected string) {
		stopPreview()
		a.cfg.OutputDevice = selected
	}
	testInput := widget.NewButton("Grabar prueba", nil)
	playRecording := widget.NewButton("Reproducir prueba", func() { a.playRecording(output.Selected) })
	testOutput := widget.NewButton("Reproducir tono", func() { a.testOutput(output.Selected) })
	showQR := widget.NewButton("Mostrar QR", func() {
		a.showPhoneMic()
		refreshPhoneStatus()
	})
	stopPhone := widget.NewButton("Detener celular", func() {
		a.phoneMicMu.RLock()
		server := a.phoneMic
		a.phoneMicMu.RUnlock()
		if server != nil {
			a.stopPhoneMic()
			a.cfg.InputDevice = phoneMicInput
			a.setStatus("Servidor de micrófono de celular detenido.")
		}
		refreshPhoneStatus()
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
		if a.transport != nil {
			a.setStatus("La prueba de micrófono solo está disponible fuera de una sala.")
			return
		}
		if monitor != nil {
			monitor.Stop()
			monitor = nil
		}
		if input.Selected == phoneMicInput {
			if !a.phoneMicEnabled() {
				a.setStatus("Primero iniciá el servidor y conectá el celular.")
				startMonitor(input.Selected)
				return
			}
			player, err := audio.NewPlayer(output.Selected)
			if err != nil {
				a.setStatus("No se pudo iniciar la escucha: " + err.Error())
				startMonitor(input.Selected)
				return
			}
			a.setPreviewPlayer(player)
		} else {
			var engine *audio.Engine
			var err error
			engine, err = audio.NewEngine(input.Selected, output.Selected, func(frame []byte) {
				payload, shouldPlay := a.prepareOutgoingPCM(frame)
				if shouldPlay && engine != nil {
					engine.Play(payload)
				}
			})
			if err != nil {
				a.setStatus("No se pudo iniciar la escucha: " + err.Error())
				startMonitor(input.Selected)
				return
			}
			a.previewMu.Lock()
			a.previewEngine = engine
			a.previewMu.Unlock()
		}
		previewActive = true
		previewButton.SetText("Dejar de escuchar")
		a.setStatus("Escuchando el micrófono. Ajustá filtro y VAD para probar.")
	})
	applyBuffer := func() bool {
		bufferMS, parseErr := strconv.Atoi(strings.TrimSpace(phoneBufferMS.Text))
		if parseErr != nil || bufferMS < 40 || bufferMS > 1000 {
			a.setStatus("El buffer del celular debe estar entre 40 y 1000 ms.")
			return false
		}
		a.cfg.PhoneMicBufferMS = bufferMS
		return true
	}
	bufferSave.OnTapped = func() {
		if !applyBuffer() {
			return
		}
		if err := config.Save(a.cfgPath, a.cfg); err != nil {
			logging.Errorf("guardar buffer de celular: %s", logging.FormatError(err))
			a.setStatus("No se pudo guardar el buffer del celular.")
			return
		}
		a.setStatus("Buffer guardado. Reiniciá el servidor del celular para aplicarlo.")
	}
	testInput.OnTapped = func() {
		if a.transport != nil {
			a.setStatus("No se puede grabar prueba mientras estás en una sala.")
			return
		}
		if !recordingTest.CompareAndSwap(false, true) {
			return
		}
		testInput.Disable()
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
		phoneSelected := input.Selected == phoneMicInput
		a.phoneMicMu.RLock()
		server := a.phoneMic
		a.phoneMicMu.RUnlock()
		listening := server != nil
		connected := listening && server.Connected()
		fyne.Do(func() {
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
				if !connected || recordingTest.Load() {
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
				if recordingTest.Load() {
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
			close(phoneStatusStop)
			go func() {
				stopPreview()
				if monitor != nil {
					monitor.Stop()
				}
			}()
			a.settingsStatus = nil
			a.window.SetContent(a.mainView)
		})
	}
	roomPortEntry := widget.NewEntry()
	cleanPort := strings.TrimPrefix(a.cfg.ListenAddress, ":")
	if cleanPort == "" || cleanPort == "0" {
		cleanPort = "47830"
	}
	roomPortEntry.SetText(cleanPort)

	phonePortEntry := widget.NewEntry()
	phonePortVal := a.cfg.PhoneMicPort
	if phonePortVal <= 0 {
		phonePortVal = 47831
	}
	phonePortEntry.SetText(strconv.Itoa(phonePortVal))

	saveSettings := func() {
		if !applyBuffer() {
			return
		}
		oldInput := a.cfg.InputDevice
		oldOutput := a.cfg.OutputDevice
		a.cfg.Username = strings.TrimSpace(username.Text)
		if a.cfg.Username == "" {
			a.cfg.Username = "Usuario"
		}
		if a.selfNameLabel != nil {
			a.selfNameLabel.SetText(a.cfg.Username)
		}
		for i := range a.roomState.Participants {
			if a.roomState.Participants[i].ID == a.participantID {
				a.roomState.Participants[i].CanBeHost = a.cfg.AllowHostMigration
				break
			}
		}
		portStr := strings.TrimSpace(roomPortEntry.Text)
		if portStr != "" {
			if !strings.HasPrefix(portStr, ":") && !strings.Contains(portStr, ":") {
				a.cfg.ListenAddress = ":" + portStr
			} else {
				a.cfg.ListenAddress = portStr
			}
		} else {
			a.cfg.ListenAddress = ":47830"
		}
		if p, err := strconv.Atoi(strings.TrimSpace(phonePortEntry.Text)); err == nil && p > 0 && p < 65536 {
			a.cfg.PhoneMicPort = p
		}
		if a.bottomBar != nil {
			if a.cfg.ShowStatusBar {
				a.bottomBar.Show()
			} else {
				a.bottomBar.Hide()
			}
		}
		if a.audioEngine != nil {
			a.audioEngine.SetJitterBufferMS(a.cfg.JitterBufferMS)
		}
		a.setStatus("Configuración guardada.")
		closeSettings()

		go func() {
			if err := config.Save(a.cfgPath, a.cfg); err != nil {
				logging.Errorf("guardar configuración: %s", logging.FormatError(err))
				fyne.Do(func() {
					a.setStatus("No se pudo guardar la configuración.")
				})
				return
			}
			if a.transport != nil && (oldInput != a.cfg.InputDevice || oldOutput != a.cfg.OutputDevice) {
				a.restartAudioInRoom()
			}
		}()
	}
	outputFilterCheck := widget.NewCheck("Aplicar filtro de salida", func(enabled bool) { a.cfg.OutputFilterEnabled = enabled })
	outputFilterCheck.SetChecked(a.cfg.OutputFilterEnabled)
	outputHighPassCheck := widget.NewCheck("Pasa-altos", func(enabled bool) { a.cfg.OutputHighPassEnabled = enabled })
	outputHighPassCheck.SetChecked(a.cfg.OutputHighPassEnabled)
	outputLowPassCheck := widget.NewCheck("Pasa-bajos", func(enabled bool) { a.cfg.OutputLowPassEnabled = enabled })
	outputLowPassCheck.SetChecked(a.cfg.OutputLowPassEnabled)
	outputLimiterCheck := widget.NewCheck("Limitador", func(enabled bool) { a.cfg.OutputLimiterEnabled = enabled })
	outputLimiterCheck.SetChecked(a.cfg.OutputLimiterEnabled)
	outputGain, outputGainLabel := newSlider("Ganancia salida (dB)", -24, 24, float64(a.cfg.OutputGainDB), func(value float64) { a.cfg.OutputGainDB = float32(value) })
	outputHighPass, outputHighPassLabel := newSlider("Frecuencia pasa-altos (Hz)", 20, 500, float64(a.cfg.OutputHighPassHz), func(value float64) { a.cfg.OutputHighPassHz = float32(value) })
	outputLowPass, outputLowPassLabel := newSlider("Frecuencia pasa-bajos (Hz)", 1000, 18000, float64(a.cfg.OutputLowPassHz), func(value float64) { a.cfg.OutputLowPassHz = float32(value) })
	outputLimiter, outputLimiterLabel := newSlider("Techo limitador (dB)", -12, 0, float64(a.cfg.OutputLimiterDB), func(value float64) { a.cfg.OutputLimiterDB = float32(value) })
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
		a.cfg.OutputFilterEnabled, a.cfg.OutputGainDB = defaults.OutputFilterEnabled, defaults.OutputGainDB
		a.cfg.OutputHighPassEnabled, a.cfg.OutputHighPassHz = defaults.OutputHighPassEnabled, defaults.OutputHighPassHz
		a.cfg.OutputLowPassEnabled, a.cfg.OutputLowPassHz = defaults.OutputLowPassEnabled, defaults.OutputLowPassHz
		a.cfg.OutputLimiterEnabled, a.cfg.OutputLimiterDB = defaults.OutputLimiterEnabled, defaults.OutputLimiterDB
		outputFilterCheck.SetChecked(a.cfg.OutputFilterEnabled)
		outputHighPassCheck.SetChecked(a.cfg.OutputHighPassEnabled)
		outputLowPassCheck.SetChecked(a.cfg.OutputLowPassEnabled)
		outputLimiterCheck.SetChecked(a.cfg.OutputLimiterEnabled)
		outputGain.SetValue(float64(a.cfg.OutputGainDB))
		outputHighPass.SetValue(float64(a.cfg.OutputHighPassHz))
		outputLowPass.SetValue(float64(a.cfg.OutputLowPassHz))
		outputLimiter.SetValue(float64(a.cfg.OutputLimiterDB))
		a.setStatus("Filtros de salida restaurados a valores de fábrica.")
	})
	showStatusBarCheck := widget.NewCheck("Mostrar barra de estado inferior", func(checked bool) { a.cfg.ShowStatusBar = checked })
	showStatusBarCheck.SetChecked(a.cfg.ShowStatusBar)
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
	eventSoundsCheck := widget.NewCheck("Sonidos de eventos (conexión, desconexión, chat)", func(checked bool) { a.cfg.EventSoundsEnabled = checked })
	eventSoundsCheck.SetChecked(a.cfg.EventSoundsEnabled)
	allowHostCheck := widget.NewCheck("Permitir ser Host de respaldo si el Host se desconecta", func(checked bool) { a.cfg.AllowHostMigration = checked })
	allowHostCheck.SetChecked(a.cfg.AllowHostMigration)

	jitterLabel := widget.NewLabel(fmt.Sprintf("Buffer de recepción (Jitter buffer): %d ms", a.cfg.JitterBufferMS))
	jitterSlider := widget.NewSlider(40, 500)
	jitterSlider.Step = 10
	jitterSlider.Value = float64(a.cfg.JitterBufferMS)
	jitterSlider.OnChanged = func(val float64) {
		a.cfg.JitterBufferMS = int(val)
		jitterLabel.SetText(fmt.Sprintf("Buffer de recepción (Jitter buffer): %d ms", int(val)))
		if a.audioEngine != nil {
			a.audioEngine.SetJitterBufferMS(int(val))
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
		switch selected {
		case "1 - Debug Normal":
			a.cfg.LogLevel = logging.LevelControl
		case "2 - Debug Avanzado":
			a.cfg.LogLevel = logging.LevelTrace
		default:
			a.cfg.LogLevel = logging.LevelError
		}
		logging.SetLevel(a.cfg.LogLevel)
	})
	switch a.cfg.LogLevel {
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
	back := widget.NewButtonWithIcon("Volver", theme.NavigateBackIcon(), closeSettings)
	header := container.NewBorder(nil, nil, back, nil, widget.NewLabelWithStyle("Configuración", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))
	footer := container.NewBorder(nil, nil, a.settingsStatus, container.NewHBox(widget.NewButton("Cancelar", closeSettings), widget.NewButton("Guardar", saveSettings)), nil)
	a.window.SetContent(container.NewBorder(header, footer, nil, nil, settingsBody))
}

func (a *App) showPhoneMic() {
	a.cfg.InputDevice = phoneMicInput
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
	qr := canvas.NewImageFromResource(fyne.NewStaticResource("voxmesh-phone-mic.png", png))
	qr.FillMode = canvas.ImageFillContain
	qr.Resize(fyne.NewSize(280, 280))
	address := widget.NewMultiLineEntry()
	address.SetText(server.URL())
	address.Disable()
	content := container.NewVBox(
		widget.NewLabel("Escaneá el código QR desde la app VoxMesh Mic (misma Wi-Fi):"),
		container.NewCenter(container.NewGridWrap(fyne.NewSize(280, 280), qr)),
		address,
	)
	dialog.ShowCustom("Usar celular como micrófono", "Cerrar", content, a.window)
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

func (a *App) testInput(name string) {
	if name == phoneMicInput {
		a.testPhoneMic()
		return
	}
	a.setStatus("Grabando desde " + name + "...")
	filterEnabled := a.cfg.AudioFilterEnabled
	thresholdDB := a.cfg.ThresholdDB
	go func() {
		data, err := audio.RecordInput(name, 2*time.Second)
		if err != nil {
			a.setStatus("No se pudo grabar: " + err.Error())
			return
		}
		if filterEnabled {
			data, _, err = audio.PrepareOutgoingPCM(data, a.voiceProcessor, audio.NoiseGate{ThresholdDB: thresholdDB}, true, a.cfg.VADThreshold)
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
