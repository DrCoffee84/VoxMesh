//go:build cgo

package ui

import (
	"fmt"
	"image/color"
	"net/url"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/atotto/clipboard"

	"voxmesh/internal/version"
)

func (a *App) flashVoiceIndicator() {
	wasActive := a.voiceActive.Swap(true)
	if !wasActive && a.voiceIndicator != nil {
		fyne.Do(func() {
			a.voiceIndicator.FillColor = color.NRGBA{R: 46, G: 204, B: 113, A: 255}
			a.voiceIndicator.Refresh()
		})
	}
	a.markPeerSpeaking(a.participantID, "")
	a.voiceTimerMu.Lock()
	if a.voiceTimer != nil {
		a.voiceTimer.Stop()
	}
	a.voiceTimer = time.AfterFunc(350*time.Millisecond, func() {
		a.voiceTimerMu.Lock()
		a.voiceActive.Store(false)
		a.voiceTimerMu.Unlock()
		if a.voiceIndicator != nil {
			fyne.Do(func() {
				a.voiceIndicator.FillColor = color.NRGBA{R: 100, G: 110, B: 120, A: 255}
				a.voiceIndicator.Refresh()
			})
		}
		a.sweepSpeakingPeers()
	})
	a.voiceTimerMu.Unlock()
}

func newScrollableEntry() *widget.Entry {
	e := widget.NewEntry()
	e.Wrapping = fyne.TextWrapOff
	e.Scroll = fyne.ScrollNone
	return e
}

type chatInputEntry struct {
	widget.Entry
	app *App
}

func newChatInputEntry(app *App) *chatInputEntry {
	e := &chatInputEntry{app: app}
	e.Wrapping = fyne.TextWrapOff
	e.Scroll = fyne.ScrollNone
	e.ExtendBaseWidget(e)
	return e
}

func (e *chatInputEntry) TypedShortcut(s fyne.Shortcut) {
	if _, ok := s.(*fyne.ShortcutPaste); ok {
		if e.app != nil && e.app.tryPasteImage() {
			return
		}
	}
	e.Entry.TypedShortcut(s)
}

func (a *App) content() fyne.CanvasObject {
	a.status = widget.NewLabel("Listo. Crea una sala o conecta con un host.")
	buildVersion := widget.NewLabel(version.Value)
	openLogs := widget.NewButton("📄", func() {
		if err := openLogsDirectory(); err != nil {
			a.setStatus("No se pudo abrir la carpeta de logs: " + err.Error())
		}
	})
	endpoint := newScrollableEntry()
	endpoint.SetPlaceHolder("IP del host (puerto por defecto 47830)")
	if a.cfg.LastPeer != "" {
		endpoint.SetText(a.cfg.LastPeer)
	}
	endpoint.OnSubmitted = func(text string) { a.connectClient(text) }
	a.endpointEntry = endpoint
	a.connectButton = widget.NewButton("Conectar", func() { a.connectClient(endpoint.Text) })
	a.leaveButton = widget.NewButton("Salir de la sala", a.stopConnection)
	a.leaveButton.Disable()
	a.createButton = widget.NewButton("Iniciar sala", a.createHost)
	a.stopButton = widget.NewButton("Parar sala", a.stopConnection)
	a.stopButton.Disable()
	copy := widget.NewButton("Copiar dirección", func() {
		if a.endpoint == "" {
			a.setStatus("Primero crea una sala.")
			return
		}
		if err := clipboard.WriteAll(a.endpoint); err != nil {
			a.setStatus("No se pudo copiar: " + err.Error())
			return
		}
		a.setStatus("Dirección copiada: " + a.endpoint)
	})

	chatInput := newChatInputEntry(a)
	chatInput.SetPlaceHolder("Escribe un mensaje...")
	chatInput.OnSubmitted = func(text string) { a.submitChat() }
	a.chatInput = &chatInput.Entry

	sendChat := widget.NewButton("Enviar", a.submitChat)
	attachImage := widget.NewButton("📎", a.selectChatImage)

	a.stagedLabel = widget.NewLabel("")
	clearStagedBtn := widget.NewButtonWithIcon("", theme.CancelIcon(), a.clearStagedImage)
	clearStagedBtn.Importance = widget.LowImportance
	a.stagedBar = container.NewBorder(nil, nil, a.stagedLabel, clearStagedBtn)
	a.stagedBar.Hide()

	composerInput := container.NewVBox(a.stagedBar, chatInput)
	chatComposer := container.NewBorder(nil, nil, attachImage, sendChat, composerInput)

	a.chatBox = container.NewVBox()
	a.chatScroll = container.NewVScroll(a.chatBox)
	a.roomSelect = widget.NewSelect(nil, func(selected string) { a.viewRoom(selected) })
	a.roomSelect.PlaceHolder = "Ver otra sala..."

	hostContent := container.NewVBox(
		widget.NewLabelWithStyle("Host", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		a.createButton,
		a.stopButton,
		copy,
	)
	a.hostPanel = uiCard(hostContent)

	clientContent := container.NewVBox(
		widget.NewLabelWithStyle("Cliente", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		endpoint,
		a.connectButton,
		a.leaveButton,
	)
	a.clientPanel = uiCard(clientContent)

	topControls := container.NewVBox(a.hostPanel, a.clientPanel)

	a.activityBox = container.NewVBox()
	a.activityScroll = container.NewVScroll(a.activityBox)
	a.activityScroll.SetMinSize(fyne.NewSize(180, 120))
	clearLogsBtn := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {
		fyne.Do(func() {
			if a.activityBox != nil {
				a.activityBox.Objects = nil
				a.activityBox.Refresh()
			}
		})
	})
	clearLogsBtn.Importance = widget.LowImportance
	activityHeader := container.NewBorder(nil, nil, widget.NewLabelWithStyle("Registro de actividad", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), clearLogsBtn)
	activityCard := uiCard(container.NewBorder(activityHeader, nil, nil, nil, a.activityScroll))

	a.connectionPanel = container.NewBorder(topControls, nil, nil, nil, activityCard)

	a.peerHBox = container.NewHBox()
	a.refreshPeerAvatars()
	peerLabel := widget.NewLabelWithStyle("En sala:", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	peerScroll := container.NewHScroll(a.peerHBox)
	peerScroll.SetMinSize(fyne.NewSize(120, 38))
	peersRow := container.NewBorder(nil, nil, peerLabel, nil, peerScroll)
	statusCard := uiCard(peersRow)

	a.chatSearch = newScrollableEntry()
	a.chatSearch.SetPlaceHolder("🔍 Buscar en el chat...")
	go func() { a.chatSearch.OnChanged = func(string) { a.refreshChat() } }()
	a.chatSearchCount = widget.NewLabel("")
	clearSearch := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		a.chatSearch.SetText("")
	})
	searchBar := container.NewBorder(nil, nil, nil, container.NewHBox(a.chatSearchCount, clearSearch), a.chatSearch)
	chatTop := container.NewBorder(nil, nil, widget.NewLabelWithStyle("Chat de la sala", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), nil, a.roomSelect)
	chatHeader := container.NewVBox(chatTop, searchBar)

	chatCard := uiCard(container.NewBorder(chatHeader, chatComposer, nil, nil, a.chatScroll))

	a.buildSoundPanel()
	toggleRoomsBtn := widget.NewButtonWithIcon("", theme.MenuIcon(), a.toggleConnectionPanels)
	toggleRoomsBtn.Importance = widget.LowImportance

	hangupRedIcon := fyne.NewStaticResource("call_end.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24"><path fill="#ba2c2c" d="M12 9c-1.6 0-3.15.25-4.6.72v3.1c0 .39-.23.74-.56.9-.98.49-1.87 1.12-2.66 1.85-.18.18-.43.28-.7.28-.28 0-.53-.11-.71-.29L.29 13.08c-.18-.17-.29-.42-.29-.7 0-.28.11-.53.29-.71C3.34 8.78 7.46 7 12 7s8.66 1.78 11.71 4.67c.18.18.29.43.29.71 0 .28-.11.53-.29.71l-2.48 2.48c-.18.18-.43.29-.71.29-.27 0-.52-.11-.7-.28-.79-.73-1.68-1.36-2.66-1.85-.33-.16-.56-.5-.56-.9v-3.1C15.15 9.25 13.6 9 12 9z"/></svg>`))
	a.topHangupBtn = widget.NewButtonWithIcon("", hangupRedIcon, a.stopConnection)
	a.topHangupBtn.Importance = widget.LowImportance
	a.topHangupBtn.Disable()

	title := widget.NewLabelWithStyle("VoxMesh", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})

	settingsBtn := widget.NewButtonWithIcon("", theme.SettingsIcon(), a.showSettings)
	settingsBtn.Importance = widget.LowImportance
	count := 0
	resetAudioBtn := widget.NewButtonWithIcon("Refresh Audio", theme.ViewRefreshIcon(), func() {
		if count == 15 {
			if u, err := url.Parse("https://www.youtube.com/watch?v=dQw4w9WgXcQ"); err == nil {
				fyne.CurrentApp().OpenURL(u)
			}
		}
		count++
		go a.restartAudioInRoom()
	})
	resetAudioBtn.Importance = widget.LowImportance

	leftTop := container.NewHBox(toggleRoomsBtn, a.topHangupBtn, title)
	topBar := container.NewBorder(nil, nil, leftTop, container.NewHBox(resetAudioBtn, settingsBtn), leftTop)
	centerPanel := container.NewBorder(statusCard, nil, nil, nil, chatCard)
	main := container.NewBorder(nil, nil, a.connectionPanel, a.soundPanel, centerPanel)
	a.bottomBar = container.NewBorder(nil, nil, a.status, container.NewHBox(buildVersion, openLogs), nil)
	if !a.cfg.ShowStatusBar {
		a.bottomBar.Hide()
	}
	a.mainView = container.NewBorder(topBar, a.bottomBar, nil, nil, main)
	return a.mainView
}

func uiCard(content fyne.CanvasObject) *fyne.Container {
	background := canvas.NewRectangle(color.NRGBA{R: 32, G: 36, B: 43, A: 255})
	background.CornerRadius = 6
	card := container.NewStack(background, container.NewPadded(content))
	return container.NewPadded(card)
}

func (a *App) toggleHostPanel() {
	a.toggleConnectionPanels()
}

func (a *App) hideConnectionPanels() {
	if a.hostPanel != nil {
		a.hostPanel.Hide()
	}
	if a.clientPanel != nil {
		a.clientPanel.Hide()
	}
	if a.connectionPanel != nil {
		a.connectionPanel.Hide()
	}
}

func (a *App) showConnectionPanels() {
	if a.hostPanel != nil {
		a.hostPanel.Show()
	}
	if a.clientPanel != nil {
		a.clientPanel.Show()
	}
	if a.connectionPanel != nil {
		a.connectionPanel.Show()
	}
}

func (a *App) toggleConnectionPanels() {
	if (a.hostPanel != nil && a.hostPanel.Visible()) || (a.clientPanel != nil && a.clientPanel.Visible()) || (a.connectionPanel != nil && a.connectionPanel.Visible()) {
		a.hideConnectionPanels()
	} else {
		a.showConnectionPanels()
	}
}

type tappableImage struct {
	widget.BaseWidget
	image    *canvas.Image
	onTapped func()
}

func newTappableImage(img *canvas.Image, onTapped func()) *tappableImage {
	t := &tappableImage{image: img, onTapped: onTapped}
	t.ExtendBaseWidget(t)
	return t
}

func (t *tappableImage) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(t.image)
}

func (t *tappableImage) Tapped(_ *fyne.PointEvent) {
	if t.onTapped != nil {
		t.onTapped()
	}
}

func (t *tappableImage) MinSize() fyne.Size {
	if t.image != nil {
		return t.image.MinSize()
	}
	return fyne.NewSize(0, 0)
}

func (a *App) showLightbox(imagePath string) {
	if a.window == nil {
		return
	}
	cSize := a.window.Canvas().Size()
	targetW := float32(float64(cSize.Width) * 0.85)
	targetH := float32(float64(cSize.Height) * 0.85)
	if targetW < 320 {
		targetW = 320
	}
	if targetH < 220 {
		targetH = 220
	}

	largeImg := &canvas.Image{File: imagePath, FillMode: canvas.ImageFillContain}
	largeImg.SetMinSize(fyne.NewSize(targetW, targetH))

	var pop *widget.PopUp
	tapImg := newTappableImage(largeImg, func() {
		if pop != nil {
			pop.Hide()
		}
	})
	pop = widget.NewPopUp(tapImg, a.window.Canvas())
	posX := (cSize.Width - targetW) / 2
	posY := (cSize.Height - targetH) / 2
	if posX < 0 {
		posX = 0
	}
	if posY < 0 {
		posY = 0
	}
	pop.ShowAtPosition(fyne.NewPos(posX, posY))
}

func formatPeerLag(p peerView) (string, color.Color) {
	if p.isSelf {
		return "Local", color.NRGBA{R: 70, G: 210, B: 120, A: 255}
	}
	if p.lag <= 0 {
		return "... ms", color.NRGBA{R: 160, G: 170, B: 185, A: 255}
	}
	ms := p.lag.Milliseconds()
	str := fmt.Sprintf("%d ms", ms)
	if ms < 80 {
		return "🟢 " + str, color.NRGBA{R: 46, G: 204, B: 113, A: 255}
	} else if ms <= 200 {
		return "🟡 " + str, color.NRGBA{R: 241, G: 196, B: 15, A: 255}
	} else if ms <= 400 {
		return "🟠 " + str, color.NRGBA{R: 230, G: 126, B: 34, A: 255}
	}
	return "🔴 " + str, color.NRGBA{R: 231, G: 76, B: 60, A: 255}
}

func (a *App) refreshPeerAvatars() {
	if a.peerHBox == nil {
		return
	}

	// 1. Clonar los datos rápidamente bajo lock y liberarlo INMEDIATAMENTE
	// Esto evita que el loop de red colisione con el hilo gráfico (Fyne).
	a.peerMu.RLock()
	peers := make([]peerView, len(a.peerNames))
	copy(peers, a.peerNames)
	a.peerMu.RUnlock()

	// 2. Ejecutar la actualización visual asincrónica en Fyne
	fyne.Do(func() {
		if a.peerHBox == nil {
			return
		}
		if len(peers) == 0 {
			a.peerHBox.Objects = []fyne.CanvasObject{widget.NewLabel("Sin usuarios")}
			a.peerHBox.Refresh()
			return
		}

		now := time.Now()
		if len(a.peerHBox.Objects) == len(peers) {
			canUpdateInPlace := true
			for i, peer := range peers {
				chip, ok := a.peerHBox.Objects[i].(*fyne.Container)
				if !ok || len(chip.Objects) < 2 {
					canUpdateInPlace = false
					break
				}
				padded, ok := chip.Objects[1].(*fyne.Container)
				if !ok || len(padded.Objects) == 0 {
					canUpdateInPlace = false
					break
				}
				inner, ok := padded.Objects[0].(*fyne.Container)
				if !ok || len(inner.Objects) < 2 {
					canUpdateInPlace = false
					break
				}
				avatarBox, ok := inner.Objects[0].(*fyne.Container)
				if !ok || len(avatarBox.Objects) == 0 {
					canUpdateInPlace = false
					break
				}
				avatarStack, ok := avatarBox.Objects[0].(*fyne.Container)
				if !ok || len(avatarStack.Objects) < 2 {
					canUpdateInPlace = false
					break
				}
				circle, okCircle := avatarStack.Objects[0].(*canvas.Circle)
				center, okCenter := avatarStack.Objects[1].(*fyne.Container)
				if !okCircle || !okCenter || len(center.Objects) == 0 {
					canUpdateInPlace = false
					break
				}
				txt, okTxt := center.Objects[0].(*canvas.Text)
				infoBox, okInfo := inner.Objects[1].(*fyne.Container)
				if !okTxt || !okInfo || len(infoBox.Objects) < 2 {
					canUpdateInPlace = false
					break
				}
				nameTxt, okName := infoBox.Objects[0].(*canvas.Text)
				lagTxt, okLag := infoBox.Objects[1].(*canvas.Text)
				if !okName || !okLag {
					canUpdateInPlace = false
					break
				}

				isSpeaking := peer.speaking && now.Before(peer.speakStop)
				targetCircleColor := color.NRGBA{R: 60, G: 70, B: 85, A: 255}
				targetTxtColor := color.NRGBA{R: 240, G: 245, B: 255, A: 255}
				if isSpeaking {
					targetCircleColor = color.NRGBA{R: 46, G: 204, B: 113, A: 255}
					targetTxtColor = color.NRGBA{R: 15, G: 35, B: 15, A: 255}
				}
				if circle.FillColor != targetCircleColor {
					circle.FillColor = targetCircleColor
					circle.Refresh()
				}
				if txt.Color != targetTxtColor {
					txt.Color = targetTxtColor
					txt.Refresh()
				}

				targetLagStr, targetLagColor := formatPeerLag(peer)
				if lagTxt.Text != targetLagStr || lagTxt.Color != targetLagColor {
					lagTxt.Text = targetLagStr
					lagTxt.Color = targetLagColor
					lagTxt.Refresh()
				}

				displayName := peer.name
				if len([]rune(displayName)) > 13 {
					displayName = string([]rune(displayName)[:11]) + "..."
				}
				if nameTxt.Text != displayName {
					nameTxt.Text = displayName
					nameTxt.Refresh()
				}
			}
			if canUpdateInPlace {
				return
			}
		}

		objects := make([]fyne.CanvasObject, 0, len(peers))
		for _, peer := range peers {
			p := peer
			initial := "U"
			trimmed := strings.TrimSpace(p.name)
			if len(trimmed) > 0 {
				initial = strings.ToUpper(string([]rune(trimmed)[:1]))
			}
			isSpeaking := p.speaking && now.Before(p.speakStop)
			bg := canvas.NewCircle(color.NRGBA{R: 60, G: 70, B: 85, A: 255})
			bg.Resize(fyne.NewSize(26, 26))
			txt := canvas.NewText(initial, color.NRGBA{R: 240, G: 245, B: 255, A: 255})
			if isSpeaking {
				bg.FillColor = color.NRGBA{R: 46, G: 204, B: 113, A: 255}
				txt.Color = color.NRGBA{R: 15, G: 35, B: 15, A: 255}
			}
			txt.TextStyle = fyne.TextStyle{Bold: true}
			txt.TextSize = 12
			txt.Alignment = fyne.TextAlignCenter
			avatarStack := container.NewStack(bg, container.NewCenter(txt))
			avatarBox := container.NewGridWrap(fyne.NewSize(26, 26), avatarStack)

			displayName := p.name
			if len([]rune(displayName)) > 13 {
				displayName = string([]rune(displayName)[:11]) + "..."
			}
			nameTxt := canvas.NewText(displayName, color.NRGBA{R: 235, G: 240, B: 250, A: 255})
			nameTxt.TextSize = 11
			nameTxt.TextStyle = fyne.TextStyle{Bold: true}

			lagStr, lagColor := formatPeerLag(p)
			lagTxt := canvas.NewText(lagStr, lagColor)
			lagTxt.TextSize = 9

			infoBox := container.NewVBox(nameTxt, lagTxt)
			chipInner := container.NewHBox(avatarBox, infoBox)

			pillBg := canvas.NewRectangle(color.NRGBA{R: 36, G: 42, B: 52, A: 255})
			pillBg.CornerRadius = 14
			chip := container.NewStack(pillBg, container.NewPadded(chipInner))

			objects = append(objects, chip)
		}
		a.peerHBox.Objects = objects
		a.peerHBox.Refresh()
	})
}
