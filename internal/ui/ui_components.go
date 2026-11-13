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

	"voxmesh/internal/i18n"
	"voxmesh/internal/imagefile"
	"voxmesh/internal/logging"
	"voxmesh/internal/version"
)

var (
	// shoutColor es el rojo del modo grito (aro del avatar y barra del medidor).
	shoutColor = color.NRGBA{R: 230, G: 60, B: 60, A: 255}
	// speakingColor es el verde de "hablando": avatar, indicador de voz propia y ping bueno.
	speakingColor = color.NRGBA{R: 46, G: 204, B: 113, A: 255}
	// idleColor es el gris del indicador de voz propia apagado.
	idleColor = color.NRGBA{R: 100, G: 110, B: 120, A: 255}
	// avatarColor es el círculo del avatar de quien no habla.
	avatarColor = color.NRGBA{R: 60, G: 70, B: 85, A: 255}
	// mutedTextColor es el texto secundario de los chips (lag, volumen).
	mutedTextColor = color.NRGBA{R: 160, G: 170, B: 185, A: 255}
	// sharingColor es el fondo del banner "Estás compartiendo tu pantalla" y la insignia del avatar
	// de quien comparte: un naranja oscuro que se ve sin parecer un error.
	sharingColor = color.NRGBA{R: 176, G: 84, B: 20, A: 255}
)

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
	fill := idleColor
	if on {
		fill = speakingColor
	}
	fyne.Do(func() {
		a.voiceIndicator.FillColor = fill
		a.voiceIndicator.Refresh()
	})
}

type chatInputEntry struct {
	widget.Entry
	app *App
}

// newChatInputEntry usa el Wrapping de widget.NewEntry: el texto largo se desplaza dentro de la
// entrada. Con TextWrapOff + ScrollNone el ancho mínimo era el del texto entero y estiraba la
// columna (AGENT.md §3.D).
func newChatInputEntry(app *App) *chatInputEntry {
	e := &chatInputEntry{app: app}
	e.Wrapping = fyne.TextWrap(fyne.TextTruncateClip)
	e.ExtendBaseWidget(e)
	return e
}

// fixedWidthLayout le da a su contenido un ancho fijo y el alto que le toque: así lo que hay
// adentro (una invitación pegada, un nombre largo) nunca ensancha la columna.
type fixedWidthLayout struct{ width float32 }

func (l fixedWidthLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	var height float32
	for _, object := range objects {
		if object.Visible() {
			height = max(height, object.MinSize().Height)
		}
	}
	return fyne.NewSize(l.width, height)
}

func (l fixedWidthLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, object := range objects {
		object.Move(fyne.NewPos(0, 0))
		object.Resize(size)
	}
}

// Anchos de las columnas laterales; el centro (avatares y chat) se queda con el resto.
const (
	connectionPanelWidth = 240
	soundPanelWidth      = 280
)

func (e *chatInputEntry) TypedShortcut(s fyne.Shortcut) {
	if _, ok := s.(*fyne.ShortcutPaste); ok {
		if e.app != nil && e.app.tryPasteImage() {
			return
		}
	}
	e.Entry.TypedShortcut(s)
}

func (a *App) content() fyne.CanvasObject {
	a.status = widget.NewLabel(i18n.T("main.ready"))
	a.status.Truncation = fyne.TextTruncateEllipsis
	// La versión abre "Acerca de" (botón chato: se ve como el texto).
	buildVersion := widget.NewButton(version.Value, a.showAbout)
	buildVersion.Importance = widget.LowImportance
	openLogs := widget.NewButtonWithIcon("", theme.DocumentIcon(), func() {
		if err := openLogsDirectory(); err != nil {
			a.setStatus(i18n.M("status.open_logs_failed", err.Error()))
		}
	})
	openLogs.Importance = widget.LowImportance
	endpoint := widget.NewEntry()
	endpoint.SetPlaceHolder(i18n.T("main.invite_placeholder"))
	endpoint.OnSubmitted = func(text string) { a.connectClient(text) }
	a.endpointEntry = endpoint
	a.connectButton = widget.NewButton(i18n.T("main.connect"), func() { a.connectClient(endpoint.Text) })
	a.connectButton.Importance = widget.HighImportance
	a.leaveButton = widget.NewButton(i18n.T("main.leave_room"), a.stopConnection)
	a.leaveButton.Disable()
	a.createButton = widget.NewButton(i18n.T("main.start_room"), a.createHost)
	a.createButton.Importance = widget.HighImportance
	a.stopButton = widget.NewButton(i18n.T("main.stop_room"), a.stopConnection)
	a.stopButton.Disable()

	chatInput := newChatInputEntry(a)
	chatInput.SetPlaceHolder(i18n.T("chat.input_placeholder"))
	chatInput.OnSubmitted = func(text string) { a.submitChat() }
	a.chatInput = &chatInput.Entry

	sendChat := widget.NewButtonWithIcon(i18n.T("chat.send"), theme.MailSendIcon(), a.submitChat)
	sendChat.Importance = widget.HighImportance
	attachImage := widget.NewButtonWithIcon("", theme.MailAttachmentIcon(), a.selectChatImage)

	a.stagedLabel = widget.NewLabel("")
	a.stagedLabel.Truncation = fyne.TextTruncateEllipsis
	clearStagedBtn := widget.NewButtonWithIcon("", theme.CancelIcon(), a.clearStagedImage)
	clearStagedBtn.Importance = widget.LowImportance
	a.stagedBar = container.NewBorder(nil, nil, widget.NewIcon(theme.FileImageIcon()), clearStagedBtn, a.stagedLabel)
	a.stagedBar.Hide()

	// La imagen pegada va arriba, a todo el ancho; 📎 y Enviar quedan a la altura del mensaje.
	chatComposer := container.NewVBox(a.stagedBar, container.NewBorder(nil, nil, attachImage, sendChat, chatInput))

	a.chatBox = container.NewVBox()
	a.chatScroll = container.NewVScroll(a.chatBox)
	a.roomSelect = widget.NewSelect(nil, func(selected string) { a.viewRoom(selected) })
	a.roomSelect.PlaceHolder = i18n.T("chat.view_other_room")

	a.hostPanel = uiCard(container.NewVBox(cardTitle(i18n.T("main.host_card")), a.createButton, a.stopButton))
	a.clientPanel = uiCard(container.NewVBox(cardTitle(i18n.T("main.client_card")), endpoint, a.connectButton, a.leaveButton))
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
	activityCard := uiCard(container.NewBorder(cardHeader(i18n.T("main.activity"), clearLogsBtn), nil, nil, nil, a.activityScroll))

	a.connectionPanel = container.New(fixedWidthLayout{connectionPanelWidth}, container.NewBorder(topControls, nil, nil, nil, activityCard))

	a.peerHBox = container.NewHBox()
	a.refreshPeerAvatars()
	peerScroll := container.NewHScroll(a.peerHBox)
	peerScroll.SetMinSize(fyne.NewSize(120, 38))
	statusCard := uiCard(container.NewBorder(nil, nil, cardTitle(i18n.T("main.in_room")), nil, peerScroll))

	a.chatSearch = widget.NewEntry()
	a.chatSearch.SetPlaceHolder(i18n.T("chat.search_placeholder"))
	a.chatSearch.OnChanged = func(string) { a.refreshChat() }
	clearSearch := widget.NewButtonWithIcon("", theme.CancelIcon(), func() { a.chatSearch.SetText("") })
	clearSearch.Importance = widget.LowImportance
	a.chatSearch.ActionItem = clearSearch // como el buscador de la botonera
	a.chatSearchCount = widget.NewLabel("")
	searchBar := container.NewBorder(nil, nil, nil, a.chatSearchCount, a.chatSearch)
	chatTop := container.NewBorder(nil, nil, cardTitle(i18n.T("chat.title")), nil, a.roomSelect)
	chatHeader := container.NewVBox(chatTop, searchBar)

	chatCard := uiCard(container.NewBorder(chatHeader, chatComposer, nil, nil, a.chatScroll))

	a.tooltip = newTooltip() // antes de la botonera: la usan sus botones de atajo
	a.buildSoundPanel()
	toggleRoomsBtn := widget.NewButtonWithIcon("", theme.MenuIcon(), a.toggleConnectionPanels)
	toggleRoomsBtn.Importance = widget.LowImportance

	hangupRedIcon := fyne.NewStaticResource("call_end.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24"><path fill="#ba2c2c" d="M12 9c-1.6 0-3.15.25-4.6.72v3.1c0 .39-.23.74-.56.9-.98.49-1.87 1.12-2.66 1.85-.18.18-.43.28-.7.28-.28 0-.53-.11-.71-.29L.29 13.08c-.18-.17-.29-.42-.29-.7 0-.28.11-.53.29-.71C3.34 8.78 7.46 7 12 7s8.66 1.78 11.71 4.67c.18.18.29.43.29.71 0 .28-.11.53-.29.71l-2.48 2.48c-.18.18-.43.29-.71.29-.27 0-.52-.11-.7-.28-.79-.73-1.68-1.36-2.66-1.85-.33-.16-.56-.5-.56-.9v-3.1C15.15 9.25 13.6 9 12 9z"/></svg>`))
	a.topHangupBtn = widget.NewButtonWithIcon("", hangupRedIcon, a.stopConnection)
	a.topHangupBtn.Importance = widget.LowImportance
	a.topHangupBtn.Disable()

	title := widget.NewLabelWithStyle("VoxMesh", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	// Nombre propio e indicador de voz: los actualizan la conexión, Ajustes y flashVoiceIndicator.
	a.voiceIndicator = canvas.NewCircle(idleColor)
	voiceDot := container.NewCenter(container.NewGridWrap(fyne.NewSize(10, 10), a.voiceIndicator))
	a.selfNameLabel = widget.NewLabel(a.conf().Username)
	a.selfNameLabel.Truncation = fyne.TextTruncateEllipsis

	settingsBtn := widget.NewButtonWithIcon("", theme.SettingsIcon(), a.showSettings)
	settingsBtn.Importance = widget.LowImportance
	count := 0
	resetAudioBtn := widget.NewButtonWithIcon(i18n.T("main.restart_audio"), theme.ViewRefreshIcon(), func() {
		if count == 15 {
			if u, err := url.Parse("https://www.youtube.com/watch?v=dQw4w9WgXcQ"); err == nil {
				fyne.CurrentApp().OpenURL(u)
			}
		}
		count++
		go a.restartAudioInRoom()
	})
	resetAudioBtn.Importance = widget.LowImportance

	inviteBtn := widget.NewButtonWithIcon(i18n.T("main.invite"), theme.ContentCopyIcon(), nil)
	inviteBtn.OnTapped = func() { a.showInviteMenu(inviteBtn) }
	inviteBtn.Importance = widget.LowImportance
	a.inviteButton = inviteBtn

	a.muteButton = widget.NewButtonWithIcon("", micIcon, a.toggleMute)
	a.muteButton.Importance = widget.LowImportance
	a.refreshMuteButton()

	shareBtn, shareBanner := a.newShareControls()

	leftTop := container.NewHBox(toggleRoomsBtn, title, widget.NewSeparator(), voiceDot)
	// Controles de la llamada juntos; después los de la app.
	rightTop := container.NewHBox(inviteBtn, shareBtn, a.muteButton, a.topHangupBtn, widget.NewSeparator(), resetAudioBtn, settingsBtn)
	// El banner de "Estás compartiendo tu pantalla" va debajo, a todo el ancho (oculto si no).
	topBar := container.NewVBox(container.NewBorder(nil, nil, leftTop, rightTop, a.selfNameLabel), shareBanner)
	centerPanel := container.NewBorder(statusCard, nil, nil, nil, chatCard)
	main := container.NewBorder(nil, nil, a.connectionPanel, a.soundPanel, centerPanel)
	a.bottomBar = container.NewBorder(nil, nil, nil, container.NewHBox(buildVersion, openLogs), a.status)
	if !a.conf().ShowStatusBar {
		a.bottomBar.Hide()
	}
	// La capa de la leyenda va encima de todo y no recibe eventos (ver tooltip).
	a.mainFrame = container.NewBorder(topBar, a.bottomBar, nil, nil, main)
	a.mainView = container.NewStack(a.mainFrame, a.tooltip.layer)
	return a.mainView
}

// tooltip es la leyenda chica de la ventana principal (la usa bindButton). Va en una capa propia
// dentro del contenido y no en un PopUp de Fyne: el PopUp tapa la ventana entera, se come el
// primer clic y le saca el mouse al botón. La capa solo tiene un rectángulo y un label, que no
// reciben eventos, así que el clic y el mouse siguen llegando a lo que está abajo. Solo el hilo
// de UI.
type tooltip struct {
	layer *fyne.Container // sin layout: el Stack de mainView la estira a toda la ventana
	box   *fyne.Container
	text  *widget.Label
}

func newTooltip() *tooltip {
	t := &tooltip{text: widget.NewLabel("")}
	background := canvas.NewRectangle(theme.Color(theme.ColorNameMenuBackground))
	background.CornerRadius = theme.InputRadiusSize()
	background.StrokeColor = theme.Color(theme.ColorNameInputBorder)
	background.StrokeWidth = 1
	t.box = container.NewStack(background, t.text)
	t.box.Hide()
	t.layer = container.NewWithoutLayout(t.box)
	return t
}

// show muestra text debajo de anchor (arriba si abajo no entra), alineada a su borde derecho y
// sin salirse de la ventana.
func (t *tooltip) show(anchor fyne.CanvasObject, text string) {
	driver := fyne.CurrentApp().Driver()
	t.text.SetText(text)
	size := t.box.MinSize()
	t.box.Resize(size)
	at := driver.AbsolutePositionForObject(anchor).Subtract(driver.AbsolutePositionForObject(t.layer))
	area := t.layer.Size()
	x := at.X + anchor.Size().Width - size.Width
	y := at.Y + anchor.Size().Height + theme.Padding()
	if y+size.Height > area.Height {
		y = at.Y - size.Height - theme.Padding()
	}
	t.box.Move(fyne.NewPos(max(0, min(x, area.Width-size.Width)), max(0, y)))
	t.box.Show()
	// Se repinta por la capa: la caja arrancó oculta y Fyne solo le registra el canvas a lo
	// visible, así que su propio Refresh no marca la ventana y no se veía hasta otro repintado.
	t.layer.Refresh()
}

func (t *tooltip) hide() {
	t.box.Hide()
}

// hideTooltip oculta la leyenda (hilo de UI).
func (a *App) hideTooltip() {
	if a.tooltip != nil {
		a.tooltip.hide()
	}
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

// uiCard es una tarjeta: fondo colorNameCard (voxTheme) con el mismo margen y relleno en todas.
func uiCard(content fyne.CanvasObject) *fyne.Container {
	background := canvas.NewRectangle(theme.Color(colorNameCard))
	background.CornerRadius = 6
	card := container.NewStack(background, container.NewPadded(content))
	return container.NewPadded(card)
}

// cardTitle es el título de una tarjeta; todas lo usan para que se vean iguales.
func cardTitle(text string) *widget.Label {
	return widget.NewLabelWithStyle(text, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
}

// cardHeader es el título de una tarjeta con acciones (botones de ícono) a la derecha.
func cardHeader(text string, actions ...fyne.CanvasObject) fyne.CanvasObject {
	return container.NewBorder(nil, nil, nil, container.NewHBox(actions...), cardTitle(text))
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

	largeImg := &canvas.Image{FillMode: canvas.ImageFillContain}
	largeImg.SetMinSize(fyne.NewSize(targetW, targetH))
	loading := widget.NewLabel(i18n.T("chat.image_loading"))

	var pop *widget.PopUp
	tapImg := newTappableImage(largeImg, func() {
		if pop != nil {
			pop.Hide()
		}
	})
	pop = widget.NewPopUp(container.NewStack(tapImg, container.NewCenter(loading)), a.window.Canvas())
	posX := (cSize.Width - targetW) / 2
	posY := (cSize.Height - targetH) / 2
	if posX < 0 {
		posX = 0
	}
	if posY < 0 {
		posY = 0
	}
	pop.ShowAtPosition(fyne.NewPos(posX, posY))

	scale := a.window.Canvas().Scale()
	maxW, maxH := int(targetW*scale), int(targetH*scale)
	go func() {
		// Una decodificación a la vez: abrir y cerrar seguido no junta varias de ~100 MB.
		lightboxDecode <- struct{}{}
		defer func() { <-lightboxDecode }()
		open := false
		fyne.DoAndWait(func() { open = pop.Visible() })
		if !open {
			return
		}
		img, err := imagefile.Load(imagePath, maxW, maxH)
		if err != nil {
			logging.Errorf("lightbox %q: %s", imagePath, logging.FormatError(err))
		}
		fyne.Do(func() {
			if !pop.Visible() {
				return
			}
			if err != nil {
				loading.SetText(i18n.T("chat.image_open_failed"))
				return
			}
			loading.Hide()
			largeImg.Image = img
			largeImg.Refresh()
		})
	}()
}

// lightboxDecode limita a una la decodificación del lightbox en curso.
var lightboxDecode = make(chan struct{}, 1)

func formatPeerLag(p peerView) (string, color.Color) {
	if p.isSelf {
		return i18n.T("main.peer_local"), speakingColor
	}
	if p.lag <= 0 {
		return "… ms", mutedTextColor
	}
	ms := p.lag.Milliseconds()
	str := fmt.Sprintf("%d ms", ms)
	if p.net.known {
		// Pérdida y jitter medidos por el jitter buffer del emisor (último intervalo).
		str += fmt.Sprintf(" · %.0f%% · ±%.0f ms", p.net.lossPct, p.net.jitterMS)
	}
	if ms < 80 {
		return "● " + str, speakingColor
	} else if ms <= 200 {
		return "● " + str, color.NRGBA{R: 241, G: 196, B: 15, A: 255}
	} else if ms <= 400 {
		return "● " + str, color.NRGBA{R: 230, G: 126, B: 34, A: 255}
	}
	return "● " + str, color.NRGBA{R: 231, G: 76, B: 60, A: 255}
}

// avatarSize es el lado del círculo del avatar en la barra de participantes.
const avatarSize = 30

// newMuteBadge es la capa que va encima del avatar (avatarSize) de un participante muteado:
// el ícono de micrófono tachado en rojo sobre un círculo del color de la tarjeta, en la esquina
// inferior derecha, para que la inicial siga leyéndose. Sin layout: la Stack del avatar la
// estira al tamaño del avatar y el badge conserva su posición. refreshPeerAvatars la muestra u
// oculta según peerView.muted.
func newMuteBadge() *fyne.Container {
	bg := canvas.NewCircle(theme.Color(colorNameCard))
	muteIco := theme.NewErrorThemedResource(fyne.NewStaticResource("mic_off.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24"><path d="M19 11h-1.7c0 .74-.16 1.43-.43 2.05l1.23 1.23c.56-.98.9-2.09.9-3.28zm-4.02.17c0-.06.02-.11.02-.17V5c0-1.66-1.34-3-3-3S9 3.34 9 5v.18l5.98 5.99zM4.27 3L3 4.27l6.01 6.01V11c0 1.66 1.33 3 2.99 3 .22 0 .44-.03.65-.08l1.66 1.66c-.71.33-1.5.52-2.31.52-2.76 0-5.3-2.1-5.3-5.1H5c0 3.41 2.72 6.23 6 6.72V21h2v-3.28c.91-.13 1.77-.45 2.54-.9L19.73 21 21 19.73 4.27 3z"/></svg>`)))
	icon := canvas.NewImageFromResource(theme.NewErrorThemedResource(muteIco))
	icon.FillMode = canvas.ImageFillContain
	badge := container.NewStack(bg, icon)
	const side = 15
	badge.Resize(fyne.NewSquareSize(side))
	badge.Move(fyne.NewPos(avatarSize-side, avatarSize-side))
	return container.NewWithoutLayout(badge)
}

// newScreenBadge es la insignia de pantalla compartida: un monitor blanco sobre un círculo de
// sharingColor, arriba a la derecha del avatar (el mute va abajo). Mismo patrón que newMuteBadge.
func newScreenBadge() *fyne.Container {
	bg := canvas.NewCircle(sharingColor)
	icon := canvas.NewImageFromResource(theme.NewColoredResource(theme.ComputerIcon(), theme.ColorNameForeground))
	icon.FillMode = canvas.ImageFillContain
	badge := container.NewStack(bg, icon)
	const side = 15
	badge.Resize(fyne.NewSquareSize(side))
	badge.Move(fyne.NewPos(avatarSize-side, 0))
	layer := container.NewWithoutLayout(badge)
	layer.Hide()
	return layer
}

// peerChip son los widgets de un participante en la barra de avatares. Los toca solo el hilo
// de UI (dentro de los fyne.Do de refreshPeerAvatars y los callbacks del slider).
type peerChip struct {
	id      string
	name    string // nombre vigente: el slider guarda el volumen por nombre
	isSelf  bool
	circle  *canvas.Circle
	initial *canvas.Text
	badge   *fyne.Container
	// screenBadge: insignia de pantalla compartida; viewBtn: "Ver pantalla" (nil para el propio).
	screenBadge *fyne.Container
	viewBtn     *widget.Button
	nameTxt     *canvas.Text
	lagTxt      *canvas.Text
	volumeTxt   *canvas.Text // nil para el propio (no hay slider de uno mismo)
	object      fyne.CanvasObject
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
			empty := widget.NewLabel(i18n.T("main.no_users"))
			empty.Importance = widget.LowImportance
			a.peerHBox.Objects = []fyne.CanvasObject{empty}
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
	chip.circle = canvas.NewCircle(avatarColor)
	chip.circle.Resize(fyne.NewSquareSize(avatarSize))
	chip.initial = canvas.NewText(initial, color.NRGBA{R: 240, G: 245, B: 255, A: 255})
	chip.initial.TextStyle = fyne.TextStyle{Bold: true}
	chip.initial.TextSize = 14
	chip.initial.Alignment = fyne.TextAlignCenter
	chip.badge = newMuteBadge()
	chip.screenBadge = newScreenBadge()
	avatarStack := container.NewStack(chip.circle, container.NewCenter(chip.initial), chip.badge, chip.screenBadge)
	avatarBox := container.NewGridWrap(fyne.NewSquareSize(avatarSize), avatarStack)

	chip.nameTxt = canvas.NewText("", theme.Color(theme.ColorNameForeground))
	chip.nameTxt.TextSize = 12
	chip.nameTxt.TextStyle = fyne.TextStyle{Bold: true}
	chip.lagTxt = canvas.NewText("", mutedTextColor)
	chip.lagTxt.TextSize = 10

	var content fyne.CanvasObject = container.NewHBox(avatarBox, container.NewVBox(chip.nameTxt, chip.lagTxt))
	if !peer.isSelf {
		// "Ver pantalla" solo mientras comparte (update lo muestra u oculta).
		chip.viewBtn = widget.NewButtonWithIcon(i18n.T("screen.view"), theme.ComputerIcon(), func() { a.watchScreen(chip.id, chip.name) })
		chip.viewBtn.Importance = widget.LowImportance
		chip.viewBtn.Hide()
		content = container.NewVBox(content, a.newPeerVolumeSlider(chip), chip.viewBtn)
	}

	pillBg := canvas.NewRectangle(theme.Color(colorNameChip))
	pillBg.CornerRadius = 8
	chip.object = container.NewStack(pillBg, container.NewPadded(content))
	return chip
}

// newPeerVolumeSlider es el control de volumen individual debajo del avatar. Mientras se
// arrastra el volumen cambia al instante en el Engine; al soltar se guarda por nombre.
func (a *App) newPeerVolumeSlider(chip *peerChip) fyne.CanvasObject {
	volume := a.peerVolume(chip.id, chip.name)
	chip.volumeTxt = canvas.NewText(formatPeerVolume(volume), mutedTextColor)
	chip.volumeTxt.TextSize = 10
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
	circleColor := avatarColor
	textColor := color.NRGBA{R: 240, G: 245, B: 255, A: 255}
	if peer.speaking && now.Before(peer.speakStop) {
		circleColor = speakingColor
		textColor = color.NRGBA{R: 15, G: 35, B: 15, A: 255}
	}
	// Aro rojo mientras habla en modo grito (markPeerShouting).
	var strokeWidth float32
	if now.Before(peer.shoutUntil) {
		strokeWidth = 3
	}
	if c.circle.FillColor != circleColor || c.circle.StrokeWidth != strokeWidth {
		c.circle.FillColor = circleColor
		c.circle.StrokeColor = shoutColor
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
	if peer.sharing != c.screenBadge.Visible() {
		if peer.sharing {
			c.screenBadge.Show()
		} else {
			c.screenBadge.Hide()
		}
	}
	if c.viewBtn != nil && peer.sharing != c.viewBtn.Visible() {
		if peer.sharing {
			c.viewBtn.Show()
		} else {
			c.viewBtn.Hide()
		}
	}

	lagText, lagColor := formatPeerLag(peer)
	if c.lagTxt.Text != lagText || c.lagTxt.Color != lagColor {
		c.lagTxt.Text = lagText
		c.lagTxt.Color = lagColor
		c.lagTxt.Refresh()
	}

	displayName := peer.name
	if runes := []rune(displayName); len(runes) > 13 {
		displayName = string(runes[:12]) + "…"
	}
	if c.nameTxt.Text != displayName {
		c.nameTxt.Text = displayName
		c.nameTxt.Refresh()
	}
}
