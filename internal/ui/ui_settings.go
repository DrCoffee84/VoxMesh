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
	"voxmesh/internal/i18n"
	"voxmesh/internal/logging"
	"voxmesh/internal/room"
)

// languageLabel va igual en los dos idiomas: quien no lee el idioma actual tiene que encontrarlo.
const languageLabel = "Language / Idioma"

func (a *App) showSettings() {
	a.setStatus(i18n.M("settings.loading"))
	go func() {
		devices, err := audio.ListDevices()
		if err != nil {
			fyne.Do(func() {
				a.setStatus(i18n.M("settings.devices_failed", err.Error()))
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
	// cfg es la instantánea al abrir la vista: solo para valores iniciales de los widgets. Los
	// callbacks publican con updateConfig y, si necesitan el valor vigente, leen a.conf().
	cfg := a.conf()
	inputOptions, inputDevices, inputSelected := deviceOptions(devices.Inputs, savedInput(cfg), phoneMicInput)
	outputOptions, outputDevices, outputSelected := deviceOptions(devices.Outputs, outputChoice(cfg))
	inputDevice := func(label string) audio.Device { return deviceByLabel(inputDevices, label) }
	outputDevice := func(label string) audio.Device { return deviceByLabel(outputDevices, label) }
	openedInput, openedOutput := savedInput(cfg), outputChoice(cfg)
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
	input.SetSelected(inputSelected)
	output := widget.NewSelect(outputOptions, nil)
	output.SetSelected(outputSelected)
	// Configuración avanzada de entrada y salida: controles generados desde config.Params, con su
	// barra de perfiles arriba (ui_audio_params.go).
	inputControls := &paramControls{app: a}
	inputProfileBar, inputProfileRow := a.newProfileBar(config.InputProfile, inputControls, inputProfiles)
	filterCheck := inputControls.control("audio_filter_enabled")
	advancedFilters := container.NewVBox(inputProfileRow, inputControls.accordion(inputSections))
	advancedFilters.Hide()
	// Detalle del medidor (con Configuración avanzada abierta): cuánta voz ve el VAD contra lo que
	// exige, y el nivel después de los filtros contra el volumen mínimo de la puerta de ruido.
	// Los textos de los medidores cambian solos (y suman "SATURA"): con salto de línea, para que no
	// ensanchen la vista (en la grilla de dos columnas el más largo cuenta doble).
	vadText := widget.NewLabel(i18n.T("settings.vad_idle"))
	vadText.Wrapping = fyne.TextWrapWord
	vadBar := newLevelBar()
	postText := widget.NewLabel(i18n.T("settings.post_idle"))
	postText.Wrapping = fyne.TextWrapWord
	postBar := newLevelBar()
	postLegend := widget.NewLabel(i18n.T("settings.post_legend"))
	postLegend.Wrapping = fyne.TextWrapWord
	postLegend.Importance = widget.LowImportance
	meterDetailsLegend := widget.NewLabel(i18n.T("settings.vad_legend"))
	meterDetailsLegend.Wrapping = fyne.TextWrapWord
	meterDetailsLegend.Importance = widget.LowImportance
	meterDetails := container.NewVBox(vadText, vadBar, meterDetailsLegend)
	meterDetails.Hide()
	advancedButton := widget.NewButton(i18n.T("settings.advanced"), func() {
		if advancedFilters.Visible() {
			advancedFilters.Hide()
			meterDetails.Hide()
		} else {
			advancedFilters.Show()
			meterDetails.Show()
		}
	})
	resetFilters := widget.NewButton("↺", inputProfileBar.loadDefault)
	profileBars := []*profileBar{inputProfileBar} // + el de salida, más abajo
	otherControls := &paramControls{app: a}
	liveMonitoring := otherControls.control("live_monitoring")
	level := newLevelBar()
	level.SetValue(0)
	levelText := widget.NewLabel(i18n.T("settings.level_idle"))
	levelText.Wrapping = fyne.TextWrapWord
	levelLegend := widget.NewLabel(i18n.T("settings.level_legend"))
	levelLegend.Wrapping = fyne.TextWrapWord
	levelLegend.Importance = widget.LowImportance
	monitor := &levelMonitor{}
	var lastLevelPaint, lastClip, lastSent, lastShout atomic.Int64
	// Llega un inputMeter por frame (50 por segundo), ya procesado por la cadena de envío. Un
	// fyne.Do por frame saturaba la cola del hilo de UI y cada SetText re-layouteaba la vista: se
	// pinta a ~15 fps. Lo que pasa entre pinturas (se mandó, modo grito, saturó) se anota siempre
	// y se sostiene un rato, así un frame suelto no se pierde ni hace parpadear el color.
	onMeter := func(m inputMeter) {
		now := time.Now().UnixNano()
		if m.clipped {
			lastClip.Store(now)
		}
		if m.sent {
			lastSent.Store(now)
		}
		if m.shout {
			lastShout.Store(now)
		}
		last := lastLevelPaint.Load()
		if now-last < int64(levelPaintInterval) || !lastLevelPaint.CompareAndSwap(last, now) {
			return
		}
		fill, state := meterQuietColor, ""
		switch {
		case m.muted:
			fill, state = meterHeldColor, i18n.T("settings.meter.muted")
		case now-lastShout.Load() < int64(meterHold):
			fill, state = shoutColor, i18n.T("settings.meter.shout")
		case now-lastSent.Load() < int64(meterHold):
			fill, state = levelFillColor, i18n.T("settings.meter.sending")
		case m.db > meterQuietDB:
			fill, state = meterHeldColor, i18n.T("settings.meter.not_sending")
		}
		clipping := now-lastClip.Load() < int64(clipHold)
		fyne.Do(func() {
			cfg := a.conf()
			level.SetValue(meterPos(m.db))
			level.SetColor(fill)
			if cfg.ShoutEnabled {
				level.SetMarker(meterPos(cfg.ShoutThresholdDB), shoutColor)
			} else {
				level.SetMarker(0, nil)
			}
			paintPostMeter(cfg, m, postText, postBar)
			if meterDetails.Visible() {
				paintVADMeter(cfg, m, vadText, vadBar)
			}
			text := i18n.T("settings.level", m.db)
			if state != "" {
				text += " · " + state
			}
			levelText.Importance = widget.MediumImportance
			if clipping {
				text += i18n.T("settings.clipping")
				levelText.Importance = widget.DangerImportance
			}
			levelText.SetText(text)
		})
	}
	// Todo lo que pasa por la cadena de envío (sala, escucha de prueba, celular y el medidor
	// propio, que la corre sin mandar nada) le avisa al medidor con reportMeter.
	a.levelListener.Store(&onMeter)
	var previewButton *widget.Button
	previewActive := false
	settingsClosed := false
	// Las closures de abajo corren en el hilo de UI; abrir/cerrar dispositivos va en levelMonitor.
	startMonitor := func(label string) {
		if a.connected() {
			monitor.Stop()
			level.SetValue(0)
			levelText.SetText(i18n.T("settings.level_in_room"))
			return
		}
		if inputDevice(label).Name == phoneMicInput {
			monitor.Stop()
			level.SetValue(0)
			levelText.SetText(i18n.T("settings.level_phone"))
			return
		}
		monitor.Start(inputDevice(label), a.meterFrame)
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
			previewButton.SetText(i18n.T("settings.preview_start"))
		}
		startMonitor(input.Selected)
	}
	a.settingsStatus = widget.NewLabel("")
	a.settingsStatus.Truncation = fyne.TextTruncateEllipsis // va en el centro del pie: un estado largo no ensancha la vista
	phoneStatusStop := make(chan struct{})
	var closeOnce sync.Once
	var recordingTest atomic.Bool
	var refreshPhoneStatus func()
	lastPhoneState := "" // último estado pintado por refreshPhoneStatus (solo hilo de UI)
	input.OnChanged = func(selected string) {
		stopPreview()
		device := inputDevice(selected)
		a.updateConfig(func(c *config.Config) { c.InputDevice, c.InputDeviceID = device.Name, device.ID })
		startMonitor(selected)
		if device.Name == phoneMicInput {
			a.showPhoneMic()
		} else {
			go a.stopPhoneMic()
		}
		refreshPhoneStatus()
	}
	output.OnChanged = func(selected string) {
		stopPreview()
		device := outputDevice(selected)
		a.updateConfig(func(c *config.Config) { c.OutputDevice, c.OutputDeviceID = device.Name, device.ID })
	}
	testInput := widget.NewButton(i18n.T("settings.test_record"), nil)
	playRecording := widget.NewButton(i18n.T("settings.test_play"), func() { a.playRecording(outputDevice(output.Selected)) })
	testOutput := widget.NewButton(i18n.T("settings.test_tone"), func() { a.testOutput(outputDevice(output.Selected)) })
	showQR := widget.NewButton(i18n.T("settings.phone_show_qr"), func() {
		a.showPhoneMic()
		refreshPhoneStatus()
	})
	stopPhone := widget.NewButton(i18n.T("settings.phone_stop"), func() {
		// server.Stop() cierra el servidor HTTP/UDP y espera conexiones: fuera del hilo de UI.
		go func() {
			a.phoneMicMu.RLock()
			server := a.phoneMic
			a.phoneMicMu.RUnlock()
			if server != nil {
				a.stopPhoneMic()
				a.setStatus(i18n.M("settings.phone_stopped"))
			}
			refreshPhoneStatus()
		}()
	})
	serverLED := canvas.NewCircle(color.NRGBA{R: 210, G: 85, B: 85, A: 255})
	connectionLED := canvas.NewCircle(color.NRGBA{R: 210, G: 85, B: 85, A: 255})
	serverLabel := widget.NewLabel(i18n.T("settings.phone_server_off"))
	connectionLabel := widget.NewLabel(i18n.T("settings.phone_not_connected"))
	previewButton = widget.NewButton(i18n.T("settings.preview_start"), func() {
		if previewActive {
			stopPreview()
			return
		}
		if a.connected() {
			a.setStatus(i18n.M("settings.preview_in_room"))
			return
		}
		inputDev, outputDev := inputDevice(input.Selected), outputDevice(output.Selected)
		if inputDev.Name == phoneMicInput && !a.phoneMicEnabled() {
			a.setStatus(i18n.M("settings.preview_phone_first"))
			return
		}
		monitor.Stop()
		previewButton.Disable()
		go func() {
			err := a.startMicPreview(inputDev, outputDev)
			fyne.Do(func() {
				previewButton.Enable()
				if settingsClosed {
					stop := a.detachPreviewAudio()
					go stop()
					return
				}
				if err != nil {
					a.setStatus(i18n.M("settings.preview_failed", err.Error()))
					startMonitor(input.Selected)
					return
				}
				previewActive = true
				previewButton.SetText(i18n.T("settings.preview_stop"))
				a.setStatus(i18n.M("settings.previewing"))
			})
		}()
	})
	testInput.OnTapped = func() {
		if a.connected() {
			a.setStatus(i18n.M("settings.test_in_room"))
			return
		}
		if !recordingTest.CompareAndSwap(false, true) {
			return
		}
		testInput.Disable()
		lastPhoneState = "" // el Disable manual desincroniza el caché: forzar el próximo repintado
		a.testInput(inputDevice(input.Selected))
		duration := 2200 * time.Millisecond
		if inputDevice(input.Selected).Name == phoneMicInput {
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
			phoneSelected := inputDevice(input.Selected).Name == phoneMicInput
			state := fmt.Sprint(phoneSelected, listening, connected, recording)
			if state == lastPhoneState {
				return
			}
			lastPhoneState = state
			serverLED.FillColor = color.NRGBA{R: 210, G: 85, B: 85, A: 255}
			serverLabel.SetText(i18n.T("settings.phone_server_off"))
			connectionLED.FillColor = color.NRGBA{R: 210, G: 85, B: 85, A: 255}
			connectionLabel.SetText(i18n.T("settings.phone_not_connected"))
			if listening {
				serverLED.FillColor = color.NRGBA{R: 90, G: 205, B: 120, A: 255}
				serverLabel.SetText(i18n.T("settings.phone_server_on"))
			}
			if connected {
				connectionLED.FillColor = color.NRGBA{R: 90, G: 205, B: 120, A: 255}
				connectionLabel.SetText(i18n.T("settings.phone_connected"))
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
	// Idioma: como el nombre y los puertos, lo confirma Guardar; se aplica al reiniciar la app.
	languageNames := make([]string, len(i18n.Languages))
	languageCodes := make(map[string]string, len(i18n.Languages))
	for i, language := range i18n.Languages {
		languageNames[i] = language.Name
		languageCodes[language.Name] = string(language.Code)
	}
	languageSelect := widget.NewSelect(languageNames, nil)
	for _, language := range i18n.Languages {
		if string(language.Code) == cfg.Language {
			languageSelect.SetSelected(language.Name)
		}
	}
	languageHint := widget.NewLabel(i18n.T("settings.language.restart"))
	languageHint.Wrapping = fyne.TextWrapWord
	languageHint.Importance = widget.LowImportance
	languageHint.TextStyle = fyne.TextStyle{Italic: true}
	openedTexts := []string{username.Text, roomPortEntry.Text, phonePortEntry.Text}
	isDirty := func() bool {
		if a.conf().LiveSettings != baseline || languageCodes[languageSelect.Selected] != cfg.Language {
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
		name := room.ClampUsername(username.Text)
		if name == "" {
			name = i18n.T("default.username")
		}
		language := languageCodes[languageSelect.Selected]
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
			c.Language = language
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
		status := []i18n.Msg{i18n.M("settings.saved")}
		if len(created) > 0 {
			var names []i18n.Msg
			for i, profile := range created {
				if i > 0 {
					names = append(names, i18n.M("settings.and"))
				}
				names = append(names, i18n.Plain(profile))
			}
			status = append(status, i18n.M("settings.saved_profiles", i18n.Join(names...)))
		}
		if cfg.Language != string(i18n.Current()) {
			status = append(status, i18n.M("settings.language_saved"))
		}
		a.setStatus(i18n.Join(status...))
		closeSettings()
		if cfg.PeerVolumeMax != baseline.PeerVolumeMax {
			a.applyPeerVolumeMax()
		}
		a.saveConfig()
		// Los selects publican el dispositivo en vivo: se compara contra el que había al abrir
		// ajustes (antes se comparaba contra a.cfg ya modificado y nunca reiniciaba).
		if a.connected() && (openedInput != savedInput(cfg) || openedOutput != outputChoice(cfg)) {
			go a.restartAudioInRoom()
		}
	}
	// discardSettings vuelve LiveSettings a la base (CAS: no pisa la sala, el token, etc. que hayan
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
					a.setStatus(i18n.M("settings.phone_restart_failed", errorMsg(err)))
				}
			}()
		}
		a.setStatus(i18n.M("settings.discarded"))
		closeSettings()
	}
	askBack := func() {
		if !isDirty() {
			closeSettings()
			return
		}
		var ask *dialog.CustomDialog
		saveButton := widget.NewButton(i18n.T("common.save"), func() {
			ask.Hide()
			saveSettings()
		})
		saveButton.Importance = widget.HighImportance
		ask = dialog.NewCustomWithoutButtons(i18n.T("settings.unsaved_title"), widget.NewLabel(i18n.T("settings.unsaved_message")), a.window)
		ask.SetButtons([]fyne.CanvasObject{
			widget.NewButton(i18n.T("settings.keep_editing"), func() { ask.Hide() }),
			widget.NewButton(i18n.T("settings.discard"), func() {
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
		ask := dialog.NewConfirm(i18n.T("settings.discard_title"), i18n.T("settings.discard_message"), func(confirmed bool) {
			if confirmed {
				discardSettings()
			}
		}, a.window)
		ask.SetConfirmText(i18n.T("settings.discard"))
		ask.SetDismissText(i18n.T("settings.keep_editing"))
		ask.Show()
	}
	outputControls := &paramControls{app: a}
	outputProfileBar, outputProfileRow := a.newProfileBar(config.OutputProfile, outputControls, outputProfiles)
	outputFilterCheck := outputControls.control("output_filter_enabled")
	outputAdvanced := container.NewVBox(outputProfileRow, outputControls.accordion(outputSections))
	outputAdvanced.Hide()
	outputAdvancedButton := widget.NewButton(i18n.T("settings.advanced"), func() {
		if outputAdvanced.Visible() {
			outputAdvanced.Hide()
		} else {
			outputAdvanced.Show()
		}
	})
	resetOutputFilters := widget.NewButton("↺", outputProfileBar.loadDefault)
	profileBars = append(profileBars, outputProfileBar)
	showStatusBarCheck := otherControls.control("show_status_bar")
	showLANInvitesCheck := otherControls.control("show_lan_invites")
	profileCard := uiCard(container.NewVBox(
		widget.NewLabelWithStyle(i18n.T("settings.card.profile"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabel(i18n.T("settings.username")),
		username,
	))
	inputCard := uiCard(container.NewVBox(
		widget.NewLabelWithStyle(i18n.T("settings.card.input"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabel(i18n.T("settings.input_source")),
		input,
		levelText,
		level,
		levelLegend,
		postText,
		postBar,
		postLegend,
		meterDetails,
		container.NewHBox(filterCheck, advancedButton, resetFilters),
		advancedFilters,
		liveMonitoring,
		previewButton,
		testInput,
	))
	outputCard := uiCard(container.NewVBox(
		widget.NewLabelWithStyle(i18n.T("settings.card.output"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabel(i18n.T("settings.output_device")),
		output,
		container.NewGridWithColumns(2, testOutput, playRecording),
		container.NewHBox(outputFilterCheck, outputAdvancedButton, resetOutputFilters),
		outputAdvanced,
	))
	phoneCard := uiCard(container.NewVBox(
		widget.NewLabelWithStyle(i18n.T("settings.card.phone"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		container.NewBorder(nil, nil, container.NewGridWrap(fyne.NewSize(12, 12), serverLED), nil, serverLabel),
		container.NewBorder(nil, nil, container.NewGridWrap(fyne.NewSize(12, 12), connectionLED), nil, connectionLabel),
		widget.NewLabel(i18n.T("settings.phone_buffer_hint")),
		container.NewGridWithColumns(2, showQR, stopPhone),
	))
	eventSoundsCheck := otherControls.control("event_sounds_enabled")
	allowHostCheck := otherControls.control("allow_host_migration")
	upnpLeaseControl := otherControls.control("upnp_lease_hours")

	networkCard := uiCard(container.NewVBox(
		widget.NewLabelWithStyle(i18n.T("settings.card.network"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabel(i18n.T("settings.room_port", config.DefaultListenPort)),
		roomPortEntry,
		widget.NewLabel(i18n.T("settings.phone_port", config.DefaultPhoneMicPort)),
		phonePortEntry,
		allowHostCheck,
		upnpLeaseControl,
	))
	openDataDirButton := widget.NewButtonWithIcon(i18n.T("settings.open_data"), theme.FolderOpenIcon(), func() {
		if err := openDataDirectory(); err != nil {
			a.setStatus(i18n.M("settings.open_data_failed", err.Error()))
		}
	})
	openLogsDirButton := widget.NewButtonWithIcon(i18n.T("settings.open_logs"), theme.DocumentIcon(), func() {
		if err := openLogsDirectory(); err != nil {
			a.setStatus(i18n.M("status.open_logs_failed", err.Error()))
		}
	})
	checkUpdatesButton := widget.NewButtonWithIcon(i18n.T("settings.check_updates"), theme.ViewRefreshIcon(), func() {
		go a.checkForUpdates(true)
	})
	logLevelLabel := widget.NewLabel(i18n.T("settings.log_level"))
	// Sin "solo errores": con el nivel normal el log ya dice qué pasó (estado, dispositivos,
	// conexiones) cuando alguien que no es dev manda el archivo.
	logNormal, logAdvanced := i18n.T("settings.log_normal"), i18n.T("settings.log_advanced")
	logLevelSelect := widget.NewSelect([]string{logNormal, logAdvanced}, func(selected string) {
		level := logging.LevelControl
		if selected == logAdvanced {
			level = logging.LevelTrace
		}
		a.updateConfig(func(c *config.Config) { c.LogLevel = level })
		logging.SetLevel(level)
	})
	if cfg.LogLevel >= logging.LevelTrace {
		logLevelSelect.SetSelected(logAdvanced)
	} else {
		logLevelSelect.SetSelected(logNormal)
	}
	interfaceCard := uiCard(container.NewVBox(
		widget.NewLabelWithStyle(i18n.T("settings.card.interface"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabel(languageLabel),
		languageSelect,
		languageHint,
		showStatusBarCheck,
		showLANInvitesCheck,
		eventSoundsCheck,
		logLevelLabel,
		logLevelSelect,
		// Uno debajo del otro: en tres columnas cada una medía lo que el texto más largo.
		openDataDirButton,
		openLogsDirButton,
		checkUpdatesButton,
	))
	settingsBody := container.NewVScroll(container.NewVBox(
		profileCard,
		container.NewGridWithColumns(2, inputCard, outputCard),
		phoneCard,
		networkCard,
		interfaceCard,
	))
	back := widget.NewButtonWithIcon(i18n.T("settings.back"), theme.NavigateBackIcon(), askBack)
	header := container.NewBorder(nil, nil, back, nil, widget.NewLabelWithStyle(i18n.T("settings.title"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))
	footer := container.NewBorder(nil, nil, nil, container.NewHBox(widget.NewButton(i18n.T("common.cancel"), askCancel), widget.NewButton(i18n.T("common.save"), saveSettings)), a.settingsStatus)
	a.window.SetContent(container.NewBorder(header, footer, nil, nil, settingsBody))
}

// showPhoneMic arranca el servidor del celular (si hace falta) y muestra el QR. Arrancar el
// servidor abre sockets, puede generar el certificado TLS y guarda la config: va en segundo plano
// y solo el diálogo se arma en el hilo de UI.
func (a *App) showPhoneMic() {
	a.updateConfig(func(c *config.Config) { c.InputDevice, c.InputDeviceID = phoneMicInput, "" })
	go func() {
		server, err := a.ensurePhoneMicServer()
		if err != nil {
			a.setStatus(i18n.M("settings.phone_start_failed", errorMsg(err)))
			return
		}
		png, err := server.QRCode()
		if err != nil {
			a.setStatus(i18n.M("settings.qr_failed", err.Error()))
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
				widget.NewLabel(i18n.T("settings.qr_scan")),
				container.NewCenter(container.NewGridWrap(fyne.NewSize(280, 280), qr)),
				address,
			)
			dialog.ShowCustom(i18n.T("settings.qr_title"), i18n.T("common.close"), content, a.window)
		})
	}()
}

// levelPaintInterval limita los repintados del medidor de nivel (~15 fps).
const levelPaintInterval = 66 * time.Millisecond

// Estados del medidor de nivel (renderSettingsView): gris silencio, amarillo hay sonido pero la
// cadena no lo manda, verde se manda (levelFillColor), rojo modo grito (shoutColor).
const (
	meterQuietDB = -50                    // por debajo, silencio (gris) y no "no se envía"
	meterHold    = 150 * time.Millisecond // cuánto se sostiene "enviando" / "modo grito"
	clipHold     = time.Second            // cuánto queda el aviso "SATURA"
)

var (
	meterQuietColor     = color.NRGBA{R: 105, G: 112, B: 125, A: 255}
	meterHeldColor      = color.NRGBA{R: 235, G: 190, B: 60, A: 255}
	meterMarkerColor    = color.NRGBA{R: 235, G: 235, B: 240, A: 255}
	meterMarkerOffColor = color.NRGBA{R: 235, G: 235, B: 240, A: 70}
)

// meterPos lleva un nivel en dBFS a la posición de la barra (LevelFloorDB → 0, 0 dBFS → 1).
func meterPos(db float32) float64 {
	return float64((db - audio.LevelFloorDB) / -audio.LevelFloorDB)
}

// paintVADMeter pinta la barra del VAD (hilo de UI). El VAD es una probabilidad, no un volumen:
// por eso tiene su propia barra (0–100 %) con la exigencia como línea.
func paintVADMeter(cfg *config.Config, m inputMeter, vadText *widget.Label, vadBar *levelBar) {
	switch {
	case m.vad < 0:
		vadBar.SetValue(0)
		vadBar.SetMarker(0, nil)
		vadText.SetText(i18n.T("settings.vad_off"))
	default:
		vadBar.SetValue(float64(m.vad))
		vadColor := meterHeldColor
		if m.vad >= cfg.VADThreshold {
			vadColor = levelFillColor
		}
		vadBar.SetColor(vadColor)
		text := i18n.T("settings.vad_value", m.vad*100, cfg.VADThreshold*100)
		marker := color.Color(meterMarkerColor)
		if !cfg.VADEnabled {
			text += i18n.T("settings.vad_disabled")
			marker = meterMarkerOffColor
		}
		vadBar.SetMarker(float64(cfg.VADThreshold), marker)
		vadText.SetText(text)
	}
}

// paintPostMeter pinta la barra post-filtro (hilo de UI): lo que se manda, ya limpio. La puerta
// de ruido mide ese audio, por eso su línea va acá y no en la barra de entrada.
func paintPostMeter(cfg *config.Config, m inputMeter, postText *widget.Label, postBar *levelBar) {
	postBar.SetValue(meterPos(m.postDB))
	postColor, state := meterQuietColor, i18n.T("settings.post.not_sent")
	if m.sent {
		postColor, state = levelFillColor, i18n.T("settings.post.sent")
	}
	postBar.SetColor(postColor)
	gate := i18n.T("settings.post.gate", cfg.ThresholdDB)
	marker := color.Color(meterMarkerColor)
	if !cfg.NoiseGateEnabled {
		gate += i18n.T("settings.post.gate_off")
		marker = meterMarkerOffColor
	}
	postBar.SetMarker(meterPos(cfg.ThresholdDB), marker)
	postText.SetText(i18n.T("settings.post_value", m.postDB, state, gate))
}

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

func (m *levelMonitor) Start(device audio.Device, onFrame func(pcm []byte)) {
	old, gen := m.detach()
	go func() {
		old.Stop()
		started, err := audio.StartMonitor(device, onFrame)
		if err != nil {
			logging.Errorf("medidor de nivel %q: %s", device.Name, logging.FormatError(err))
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

// deviceOptions arma las opciones de un select de dispositivos: el predeterminado, las fijas
// (fixed: el celular, por el nombre que se guarda) y los que da Windows por nombre (si dos se
// llaman igual, el segundo lleva " (2)"). Devuelve de qué dispositivo es cada etiqueta y cuál
// mostrar elegida: la guardada (por ID; las fijas, por nombre) o, si ya no está, el predeterminado.
// No escribe la config.
func deviceOptions(devices []audio.Device, want audio.Device, fixed ...string) ([]string, map[string]audio.Device, string) {
	names := append([]string{config.DefaultDevice}, fixed...)
	options := make([]string, 0, len(names)+len(devices))
	byLabel := make(map[string]audio.Device, len(names)+len(devices))
	selected := fixedDeviceLabel(config.DefaultDevice)
	for _, name := range names {
		label := fixedDeviceLabel(name)
		options = append(options, label)
		byLabel[label] = audio.Device{Name: name}
		if want.ID == "" && want.Name == name {
			selected = label
		}
	}
	index, _ := audio.PickDevice(devices, want)
	for i, device := range devices {
		label := device.Name
		for n := 2; ; n++ {
			if _, taken := byLabel[label]; !taken {
				break
			}
			label = fmt.Sprintf("%s (%d)", device.Name, n)
		}
		byLabel[label] = device
		options = append(options, label)
		if i == index {
			selected = label
		}
	}
	return options, byLabel, selected
}

// fixedDeviceLabel es lo que muestra el select para una opción fija. El predeterminado y el celular
// se guardan en voxmesh.json con su nombre en español (son identificadores); se muestran en el
// idioma de la app.
func fixedDeviceLabel(name string) string {
	switch name {
	case config.DefaultDevice:
		return i18n.T("device.system_default")
	case phoneMicInput:
		return i18n.T("device.phone_mic")
	}
	return name
}

func deviceByLabel(byLabel map[string]audio.Device, label string) audio.Device {
	if device, ok := byLabel[label]; ok {
		return device
	}
	return audio.DefaultChoice
}

func (a *App) testPhoneMic() {
	a.phoneMicMu.RLock()
	server := a.phoneMic
	a.phoneMicMu.RUnlock()
	if server == nil {
		a.setStatus(i18n.M("settings.phone_test_first"))
		return
	}
	if !server.Connected() {
		a.setStatus(i18n.M("settings.phone_test_waiting"))
		return
	}
	server.ResetRecording()
	a.setStatus(i18n.M("settings.phone_test_recording"))
	go func() {
		time.Sleep(5 * time.Second)
		data := server.Recording()
		if len(data) == 0 {
			a.setStatus(i18n.M("settings.phone_test_no_audio"))
			return
		}
		data, err := a.processTestRecording(a.conf(), data)
		if err != nil {
			a.setStatus(i18n.M("settings.filter_failed", err.Error()))
			return
		}
		a.recordingMu.Lock()
		a.recordedInput = data
		a.recordingMu.Unlock()
		a.setStatus(i18n.M("settings.phone_test_ready"))
	}()
}

func (a *App) testInput(device audio.Device) {
	if device.Name == phoneMicInput {
		a.testPhoneMic()
		return
	}
	a.setStatus(i18n.M("settings.recording_from", deviceLabel(device)))
	go func() {
		data, err := audio.RecordInput(device, 2*time.Second)
		if err != nil {
			a.setStatus(i18n.M("settings.record_failed", err.Error()))
			return
		}
		cfg := a.conf()
		data, err = a.processTestRecording(cfg, data)
		if err != nil {
			a.setStatus(i18n.M("settings.filter_failed", err.Error()))
			return
		}
		a.recordingMu.Lock()
		a.recordedInput = data
		a.recordingMu.Unlock()
		mode := i18n.M("settings.mode_raw")
		if cfg.AudioFilterEnabled {
			mode = i18n.M("settings.mode_filtered")
		}
		a.setStatus(i18n.M("settings.mic_ok", mode, len(data)))
	}()
}

func (a *App) processTestRecording(cfg *config.Config, data []byte) ([]byte, error) {
	denoiser, err := audio.NewRNNoise()
	if err != nil {
		return nil, err
	}
	gate, settings := audio.InputSettings(cfg)
	return audio.ProcessRecording(data, denoiser, gate, cfg.AudioFilterEnabled, cfg.VADThreshold, settings)
}

func (a *App) testOutput(device audio.Device) {
	a.setStatus(i18n.M("settings.playing_tone", deviceLabel(device)))
	go func() {
		if err := audio.PlayTestTone(device, time.Second); err != nil {
			a.setStatus(i18n.M("settings.play_failed", err.Error()))
			return
		}
		a.setStatus(i18n.M("settings.output_ok"))
	}()
}

func (a *App) playRecording(device audio.Device) {
	a.recordingMu.RLock()
	data := append([]byte(nil), a.recordedInput...)
	a.recordingMu.RUnlock()
	if len(data) == 0 {
		a.setStatus(i18n.M("settings.play_first"))
		return
	}
	a.setStatus(i18n.M("settings.playing_recording"))
	go func() {
		if err := audio.PlayPCM(device, data); err != nil {
			a.setStatus(i18n.M("settings.play_recording_failed", err.Error()))
			return
		}
		a.setStatus(i18n.M("settings.recording_played"))
	}()
}
