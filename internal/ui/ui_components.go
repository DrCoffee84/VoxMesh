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

	"voxmesh/internal/config"
	"voxmesh/internal/logging"
	"voxmesh/internal/version"
)

// clipColor es el rojo de saturación (aro del avatar y medidor de nivel).
var clipColor = color.NRGBA{R: 230, G: 60, B: 60, A: 255}

// voiceHold es cuánto queda prendido el indicador de voz propia después del último frame mandado.
const voiceHold = 350 * time.Millisecond

// flashVoiceIndicator marca que se está mandando voz. Corre por cada frame enviado (50 por segundo,
// en la goroutine de envío), así que solo anota la hora: el indicador se prende con el primer frame
// y lo apaga sweepVoiceIndicator desde audioMonitorLoop. Antes creaba un timer por frame.
func (a *App) flashVoiceIndicator() {
	a.voiceLastAt.Store(time.Now().UnixNano())
	if a.voiceActive.Swap(true) {
		return
	}
	a.setVoiceIndicator(true)
	a.markPeerSpeaking(a.participantID, "")
}

// sweepVoiceIndicator apaga el indicador de voz propia cuando pasó voiceHold sin mandar voz; mientras
// sigue prendido, mantiene el avatar propio como "hablando".
func (a *App) sweepVoiceIndicator(now time.Time) {
	if !a.voiceActive.Load() {
		return
	}
	if now.Sub(time.Unix(0, a.voiceLastAt.Load())) < voiceHold {
		a.markPeerSpeaking(a.participantID, "")
		return
	}
	if a.voiceActive.CompareAndSwap(true, false) {
		a.setVoiceIndicator(false)
		a.sweepSpeakingPeers()
	}
}

func (a *App) setVoiceIndicator(on bool) {
	if a.voiceIndicator == nil {
		return
	}
	fill := color.NRGBA{R: 100, G: 110, B: 120, A: 255}
	if on {
		fill = color.NRGBA{R: 46, G: 204, B: 113, A: 255}
	}
	fyne.Do(func() {
		a.voiceIndicator.FillColor = fill
		a.voiceIndicator.Refresh()
	})
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
	endpoint.SetPlaceHolder(fmt.Sprintf("IP del host (puerto por defecto %d)", config.DefaultListenPort))
	if lastPeer := a.conf().LastPeer; lastPeer != "" {
		endpoint.SetText(lastPeer)
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
		address := a.endpointText()
		if address == "" {
			a.setStatus("Primero crea una sala.")
			return
		}
		if err := clipboard.WriteAll(address); err != nil {
			a.setStatus("No se pudo copiar: " + err.Error())
			return
		}
		a.setStatus("Dirección copiada: " + address)
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
	a.chatSearch.OnChanged = func(string) { a.refreshChat() }
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

	a.muteButton = widget.NewButtonWithIcon("", micIcon, a.toggleMute)
	a.muteButton.Importance = widget.LowImportance
	a.refreshMuteButton()

	leftTop := container.NewHBox(toggleRoomsBtn, a.topHangupBtn, title)
	topBar := container.NewBorder(nil, nil, leftTop, container.NewHBox(a.muteButton, resetAudioBtn, settingsBtn), leftTop)
	centerPanel := container.NewBorder(statusCard, nil, nil, nil, chatCard)
	main := container.NewBorder(nil, nil, a.connectionPanel, a.soundPanel, centerPanel)
	a.bottomBar = container.NewBorder(nil, nil, a.status, container.NewHBox(buildVersion, openLogs), nil)
	if !a.conf().ShowStatusBar {
		a.bottomBar.Hide()
	}
	a.mainView = container.NewBorder(topBar, a.bottomBar, nil, nil, main)
	return a.mainView
}

// Íconos del botón de mute (Material "mic" / "mic_off"). Themed: toman el color del texto;
// el tachado usa el color de error (rojo) para que se note que el micrófono está cortado.
var (
	micIcon    = theme.NewThemedResource(fyne.NewStaticResource("mic.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24"><path d="M12 14c1.66 0 2.99-1.34 2.99-3L15 5c0-1.66-1.34-3-3-3S9 3.34 9 5v6c0 1.66 1.34 3 3 3zm5.3-3c0 3-2.54 5.1-5.3 5.1S6.7 14 6.7 11H5c0 3.41 2.72 6.23 6 6.72V21h2v-3.28c3.28-.48 6-3.3 6-6.72h-1.7z"/></svg>`)))
	micOffIcon = theme.NewErrorThemedResource(fyne.NewStaticResource("mic_off.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24"><path d="M19 11h-1.7c0 .74-.16 1.43-.43 2.05l1.23 1.23c.56-.98.9-2.09.9-3.28zm-4.02.17c0-.06.02-.11.02-.17V5c0-1.66-1.34-3-3-3S9 3.34 9 5v.18l5.98 5.99zM4.27 3L3 4.27l6.01 6.01V11c0 1.66 1.33 3 2.99 3 .22 0 .44-.03.65-.08l1.66 1.66c-.71.33-1.5.52-2.31.52-2.76 0-5.3-2.1-5.3-5.1H5c0 3.41 2.72 6.23 6 6.72V21h2v-3.28c.91-.13 1.77-.45 2.54-.9L19.73 21 21 19.73 4.27 3z"/></svg>`)))
)

// toggleMute corre en el hilo de UI (botón). El corte del audio es inmediato: el callback de
// captura lee isMuted en el próximo frame. La señalización a la sala va en una goroutine.
func (a *App) toggleMute() {
	muted := !a.isMuted.Load()
	a.isMuted.Store(muted)
	a.refreshMuteButton()
	if muted {
		logging.Infof("Micrófono muteado")
	} else {
		logging.Infof("Micrófono activo")
	}
	go a.publishMuteState()
}

func (a *App) refreshMuteButton() {
	if a.muteButton == nil {
		return
	}
	if a.isMuted.Load() {
		a.muteButton.SetIcon(micOffIcon)
	} else {
		a.muteButton.SetIcon(micIcon)
	}
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
	if p.net.known {
		// Pérdida y jitter medidos por el jitter buffer del emisor (último intervalo).
		str += fmt.Sprintf(" · %.0f%% · ±%.0f ms", p.net.lossPct, p.net.jitterMS)
	}
	if ms < 80 {
		return "🟢 " + str, color.NRGBA{R: 46, G: 204, B: 113, A: 255}
	} else if ms <= 200 {
		return "🟡 " + str, color.NRGBA{R: 241, G: 196, B: 15, A: 255}
	} else if ms <= 400 {
		return "🟠 " + str, color.NRGBA{R: 230, G: 126, B: 34, A: 255}
	}
	return "🔴 " + str, color.NRGBA{R: 231, G: 76, B: 60, A: 255}
}

// newMuteBadge es la capa que va encima del avatar (26x26) de un participante muteado:
// theme.VolumeMuteIcon en rojo sobre un círculo oscuro en la esquina inferior derecha, para
// que la inicial siga leyéndose. Sin layout: la Stack del avatar la estira a 26x26 y el
// badge conserva su posición. refreshPeerAvatars la muestra u oculta según peerView.muted.
func newMuteBadge() *fyne.Container {
	bg := canvas.NewCircle(color.NRGBA{R: 24, G: 28, B: 34, A: 255})
	muteIco := theme.NewErrorThemedResource(fyne.NewStaticResource("mic_off.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24"><path d="M19 11h-1.7c0 .74-.16 1.43-.43 2.05l1.23 1.23c.56-.98.9-2.09.9-3.28zm-4.02.17c0-.06.02-.11.02-.17V5c0-1.66-1.34-3-3-3S9 3.34 9 5v.18l5.98 5.99zM4.27 3L3 4.27l6.01 6.01V11c0 1.66 1.33 3 2.99 3 .22 0 .44-.03.65-.08l1.66 1.66c-.71.33-1.5.52-2.31.52-2.76 0-5.3-2.1-5.3-5.1H5c0 3.41 2.72 6.23 6 6.72V21h2v-3.28c.91-.13 1.77-.45 2.54-.9L19.73 21 21 19.73 4.27 3z"/></svg>`)))
	icon := canvas.NewImageFromResource(theme.NewErrorThemedResource(muteIco))
	icon.FillMode = canvas.ImageFillContain
	badge := container.NewStack(bg, icon)
	badge.Resize(fyne.NewSize(14, 14))
	badge.Move(fyne.NewPos(12, 12))
	return container.NewWithoutLayout(badge)
}

// peerChip son los widgets de un participante en la barra de avatares. Los toca solo el hilo
// de UI (dentro de los fyne.Do de refreshPeerAvatars y los callbacks del slider).
type peerChip struct {
	id        string
	name      string // nombre vigente: el slider guarda el volumen por nombre
	isSelf    bool
	circle    *canvas.Circle
	initial   *canvas.Text
	badge     *fyne.Container
	nameTxt   *canvas.Text
	lagTxt    *canvas.Text
	volumeTxt *canvas.Text // nil para el propio (no hay slider de uno mismo)
	object    fyne.CanvasObject
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
			a.peerChips = nil
			a.peerHBox.Objects = []fyne.CanvasObject{widget.NewLabel("Sin usuarios")}
			a.peerHBox.Refresh()
			return
		}

		now := time.Now()
		// Mismos participantes en el mismo orden: se actualizan los widgets existentes (sin
		// relayout de toda la barra y sin cortar un arrastre del slider en curso).
		if len(a.peerChips) == len(peers) {
			same := true
			for i, peer := range peers {
				if a.peerChips[i].id != peer.id || a.peerChips[i].isSelf != peer.isSelf {
					same = false
					break
				}
			}
			if same {
				for i, peer := range peers {
					a.peerChips[i].update(peer, now)
				}
				return
			}
		}

		chips := make([]*peerChip, 0, len(peers))
		objects := make([]fyne.CanvasObject, 0, len(peers))
		for _, peer := range peers {
			chip := a.newPeerChip(peer)
			chip.update(peer, now)
			chips = append(chips, chip)
			objects = append(objects, chip.object)
		}
		a.peerChips = chips
		a.peerHBox.Objects = objects
		a.peerHBox.Refresh()
	})
}

// newPeerChip arma el chip de un participante: avatar con inicial y badge de mute, nombre, lag
// y, para los demás, el slider de volumen individual (0–300 %).
func (a *App) newPeerChip(peer peerView) *peerChip {
	chip := &peerChip{id: peer.id, name: peer.name, isSelf: peer.isSelf}
	initial := "U"
	if trimmed := strings.TrimSpace(peer.name); len(trimmed) > 0 {
		initial = strings.ToUpper(string([]rune(trimmed)[:1]))
	}
	chip.circle = canvas.NewCircle(color.NRGBA{R: 60, G: 70, B: 85, A: 255})
	chip.circle.Resize(fyne.NewSize(26, 26))
	chip.initial = canvas.NewText(initial, color.NRGBA{R: 240, G: 245, B: 255, A: 255})
	chip.initial.TextStyle = fyne.TextStyle{Bold: true}
	chip.initial.TextSize = 12
	chip.initial.Alignment = fyne.TextAlignCenter
	chip.badge = newMuteBadge()
	avatarStack := container.NewStack(chip.circle, container.NewCenter(chip.initial), chip.badge)
	avatarBox := container.NewGridWrap(fyne.NewSize(26, 26), avatarStack)

	chip.nameTxt = canvas.NewText("", color.NRGBA{R: 235, G: 240, B: 250, A: 255})
	chip.nameTxt.TextSize = 11
	chip.nameTxt.TextStyle = fyne.TextStyle{Bold: true}
	chip.lagTxt = canvas.NewText("", color.NRGBA{R: 160, G: 170, B: 185, A: 255})
	chip.lagTxt.TextSize = 9

	var content fyne.CanvasObject = container.NewHBox(avatarBox, container.NewVBox(chip.nameTxt, chip.lagTxt))
	if !peer.isSelf {
		content = container.NewVBox(content, a.newPeerVolumeSlider(chip))
	}

	pillBg := canvas.NewRectangle(color.NRGBA{R: 36, G: 42, B: 52, A: 255})
	pillBg.CornerRadius = 14
	chip.object = container.NewStack(pillBg, container.NewPadded(content))
	return chip
}

// newPeerVolumeSlider es el control de volumen individual debajo del avatar. Mientras se
// arrastra el volumen cambia al instante en el Engine; al soltar se guarda por nombre.
func (a *App) newPeerVolumeSlider(chip *peerChip) fyne.CanvasObject {
	volume := a.peerVolume(chip.id, chip.name)
	chip.volumeTxt = canvas.NewText(formatPeerVolume(volume), color.NRGBA{R: 160, G: 170, B: 185, A: 255})
	chip.volumeTxt.TextSize = 9
	slider := widget.NewSlider(0, float64(a.conf().PeerVolumeMax))
	slider.Step = 0.05
	slider.Value = volume
	slider.OnChanged = func(value float64) {
		chip.volumeTxt.Text = formatPeerVolume(value)
		chip.volumeTxt.Refresh()
		a.setPeerVolume(chip.id, chip.name, value, false)
	}
	slider.OnChangeEnded = func(value float64) {
		a.setPeerVolume(chip.id, chip.name, value, true)
	}
	sliderBox := container.NewGridWrap(fyne.NewSize(110, slider.MinSize().Height), slider)
	return container.NewHBox(sliderBox, container.NewCenter(chip.volumeTxt))
}

func formatPeerVolume(volume float64) string {
	return fmt.Sprintf("%d%%", int(volume*100+0.5))
}

// update refleja peer en los widgets del chip (solo lo que cambió, para no refrescar de más).
func (c *peerChip) update(peer peerView, now time.Time) {
	c.name = peer.name
	circleColor := color.NRGBA{R: 60, G: 70, B: 85, A: 255}
	textColor := color.NRGBA{R: 240, G: 245, B: 255, A: 255}
	if peer.speaking && now.Before(peer.speakStop) {
		circleColor = color.NRGBA{R: 46, G: 204, B: 113, A: 255}
		textColor = color.NRGBA{R: 15, G: 35, B: 15, A: 255}
	}
	// Aro rojo mientras su micrófono satura (markPeerClipping).
	var strokeWidth float32
	if now.Before(peer.clipUntil) {
		strokeWidth = 3
	}
	if c.circle.FillColor != circleColor || c.circle.StrokeWidth != strokeWidth {
		c.circle.FillColor = circleColor
		c.circle.StrokeColor = clipColor
		c.circle.StrokeWidth = strokeWidth
		c.circle.Refresh()
	}
	if c.initial.Color != textColor {
		c.initial.Color = textColor
		c.initial.Refresh()
	}
	if peer.muted != c.badge.Visible() {
		if peer.muted {
			c.badge.Show()
		} else {
			c.badge.Hide()
		}
	}

	lagText, lagColor := formatPeerLag(peer)
	if c.lagTxt.Text != lagText || c.lagTxt.Color != lagColor {
		c.lagTxt.Text = lagText
		c.lagTxt.Color = lagColor
		c.lagTxt.Refresh()
	}

	displayName := peer.name
	if len([]rune(displayName)) > 13 {
		displayName = string([]rune(displayName)[:11]) + "..."
	}
	if c.nameTxt.Text != displayName {
		c.nameTxt.Text = displayName
		c.nameTxt.Refresh()
	}
}
