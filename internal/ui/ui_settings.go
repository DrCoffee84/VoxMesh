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
		cfg := a.conf()
		inputProfiles := a.loadProfileState(config.InputProfile, cfg)
		outputProfiles := a.loadProfileState(config.OutputProfile, cfg)
		fyne.Do(func() {
			a.renderSettingsView(devices, inputProfiles, outputProfiles)
		})
	}()
}

func (a *App) renderSettingsView(devices audio.DeviceLists, inputProfiles, outputProfiles profileState) {
	inputOptions := append([]string{config.DefaultDevice, phoneMicInput}, devices.Inputs...)
	outputOptions := append([]string{config.DefaultDevice}, devices.Outputs...)
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
	// Configuración avanzada de entrada y salida: controles generados desde config.Params, con su
	// barra de perfiles arriba (ui_audio_params.go).
	inputControls := &paramControls{app: a}
	inputProfileBar, inputProfileRow := a.newProfileBar(config.InputProfile, inputControls, inputProfiles)
	filterCheck := inputControls.control("audio_filter_enabled")
	advancedFilters := container.NewVBox(inputProfileRow, inputControls.accordion(inputSections))
	advancedFilters.Hide()
	advancedButton := widget.NewButton("Configuración avanzada", func() {
		if advancedFilters.Visible() {
			advancedFilters.Hide()
		} else {
			advancedFilters.Show()
		}
	})
	resetFilters := widget.NewButton("↺", inputProfileBar.loadDefault)
	profileBars := []*profileBar{inputProfileBar} // + el de salida, más abajo
	otherControls := &paramControls{app: a}
	liveMonitoring := otherControls.control("live_monitoring")
	level := newLevelBar()
	level.SetValue(0)
	levelText := widget.NewLabel("Nivel: -60 dBFS")
	monitor := &levelMonitor{}
	var lastLevelPaint, lastClip atomic.Int64
	// malgo llama a onLevel por cada buffer de captura (~100 veces/s). Un fyne.Do por llamada
	// saturaba la cola del hilo de UI y cada SetText re-layouteaba la vista: se limita a ~15 fps.
	// La saturación se anota siempre (aunque no toque pintar) y queda en rojo clipHold.
	onLevel := func(db float32, clipped bool) {
		now := time.Now().UnixNano()
		if clipped {
			lastClip.Store(now)
		}
		last := lastLevelPaint.Load()
		if now-last < int64(levelPaintInterval) || !lastLevelPaint.CompareAndSwap(last, now) {
			return
		}
		clipping := now-lastClip.Load() < int64(clipHold)
		fyne.Do(func() {
			level.SetValue(float64((db - audio.LevelFloorDB) / -audio.LevelFloorDB))
			level.SetClipping(clipping)
			text := fmt.Sprintf("Nivel: %.0f dBFS", db)
			levelText.Importance = widget.MediumImportance
			if clipping {
				text += " · SATURA (bajá el volumen del micrófono)"
				levelText.Importance = widget.DangerImportance
			}
			levelText.SetText(text)
		})
	}
	// En sala, en la escucha de prueba y con el celular no corre el medidor propio: el nivel llega
	// de los frames de entrada (reportInputLevel).
	a.levelListener.Store(&onLevel)
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
			a.levelListener.Store(nil)
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
		cleanPort = strconv.Itoa(config.DefaultListenPort)
	}
	roomPortEntry.SetText(cleanPort)

	phonePortEntry := widget.NewEntry()
	phonePortVal := cfg.PhoneMicPort
	if phonePortVal <= 0 {
		phonePortVal = config.DefaultPhoneMicPort
	}
	phonePortEntry.SetText(strconv.Itoa(phonePortVal))
	openedTexts := []string{username.Text, roomPortEntry.Text, phonePortEntry.Text}
	isDirty := func() bool {
		if a.conf().LiveSettings != baseline {
			return true
		}
		for i, entry := range []*widget.Entry{username, roomPortEntry, phonePortEntry} {
			if strings.TrimSpace(entry.Text) != strings.TrimSpace(openedTexts[i]) {
				return true
			}
		}
		return false
	}

	saveSettings := func() {
		// Cambios hechos sobre Default: van a un perfil nuevo (Default se pisa en cada arranque).
		var created []string
		for _, bar := range profileBars {
			if profile, ok := bar.saveDefaultChangesAsProfile(); ok {
				created = append(created, fmt.Sprintf("%q", profile))
			}
		}
		name := strings.TrimSpace(username.Text)
		if name == "" {
			name = config.DefaultUsername
		}
		listenAddress := config.DefaultListenAddress
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
		status := "Configuración guardada."
		if len(created) > 0 {
			status += " Tus cambios sobre Default quedaron en el perfil " + strings.Join(created, " y ") + "."
		}
		a.setStatus(status)
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
	outputControls := &paramControls{app: a}
	outputProfileBar, outputProfileRow := a.newProfileBar(config.OutputProfile, outputControls, outputProfiles)
	outputFilterCheck := outputControls.control("output_filter_enabled")
	outputAdvanced := container.NewVBox(outputProfileRow, outputControls.accordion(outputSections))
	outputAdvanced.Hide()
	outputAdvancedButton := widget.NewButton("Configuración avanzada", func() {
		if outputAdvanced.Visible() {
			outputAdvanced.Hide()
		} else {
			outputAdvanced.Show()
		}
	})
	resetOutputFilters := widget.NewButton("↺", outputProfileBar.loadDefault)
	profileBars = append(profileBars, outputProfileBar)
	showStatusBarCheck := otherControls.control("show_status_bar")
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
		widget.NewLabel("El buffer del celular se ajusta en Entrada → Configuración avanzada."),
		container.NewGridWithColumns(2, showQR, stopPhone),
	))
	eventSoundsCheck := otherControls.control("event_sounds_enabled")
	allowHostCheck := otherControls.control("allow_host_migration")

	networkCard := uiCard(container.NewVBox(
		widget.NewLabelWithStyle("🌐 Red y Conectividad", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabel(fmt.Sprintf("Puerto UDP de la sala (predeterminado %d):", config.DefaultListenPort)),
		roomPortEntry,
		widget.NewLabel(fmt.Sprintf("Puerto UDP para el celular (predeterminado %d):", config.DefaultPhoneMicPort)),
		phonePortEntry,
		allowHostCheck,
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

func (m *levelMonitor) Start(name string, onLevel func(db float32, clipped bool)) {
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
