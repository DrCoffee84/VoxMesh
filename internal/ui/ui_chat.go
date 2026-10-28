//go:build cgo

package ui

import (
	"errors"
	"fmt"
	"image/color"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/atotto/clipboard"

	"voxmesh/internal/assets"
	"voxmesh/internal/audio"
	"voxmesh/internal/config"
	"voxmesh/internal/history"
	"voxmesh/internal/imagefile"
	"voxmesh/internal/logging"
	"voxmesh/internal/room"
	"voxmesh/internal/transport"
)

var urlRegex = regexp.MustCompile(`https?://[^\s<>"'()]+`)

func (a *App) stageImage(ext string, data []byte, name string) {
	if len(data) > assets.MaxBytes {
		a.setStatus(fmt.Sprintf("La imagen supera el límite de %d MiB.", assets.MaxBytes/1024/1024))
		return
	}
	a.stagedExt = ext
	a.stagedData = data
	a.stagedName = name
	kb := len(data) / 1024
	fyne.Do(func() {
		if a.stagedLabel != nil {
			a.stagedLabel.SetText(fmt.Sprintf("📷 Imagen lista para enviar: %s (%d KB)", name, kb))
		}
		if a.stagedBar != nil {
			a.stagedBar.Show()
		}
	})
	a.setStatus(fmt.Sprintf("Imagen adjuntada (%s, %d KB). Presiona Enviar para transmitirla.", name, kb))
}

func (a *App) clearStagedImage() {
	a.stagedExt = ""
	a.stagedData = nil
	a.stagedName = ""
	fyne.Do(func() {
		if a.stagedBar != nil {
			a.stagedBar.Hide()
		}
	})
}

func (a *App) tryPasteImage() bool {
	ext, data, name, err := readClipboardImage()
	if err != nil || len(data) == 0 {
		return false
	}
	a.stageImage(ext, data, name)
	return true
}

func (a *App) submitChat() {
	var txt string
	if a.chatInput != nil {
		txt = a.chatInput.Text
	}
	hasText := strings.TrimSpace(txt) != ""
	hasImage := len(a.stagedData) > 0

	if !hasText && !hasImage {
		return
	}

	if !a.connected() {
		a.setStatus("Conecta a una sala antes de enviar un mensaje.")
		return
	}

	if a.chatInput != nil {
		a.chatInput.SetText("")
	}

	if hasImage {
		ext := a.stagedExt
		data := a.stagedData
		a.clearStagedImage()
		go a.sendChatImageBytes(ext, data, txt)
		return
	}

	if hasText {
		a.sendChatMessage(txt)
	}
}

const unsupportedImageStatus = "Formato de imagen no soportado (PNG, JPG, GIF o WebP)."

// imageSendable rechaza antes de mandarla una imagen que los demás tirarían al recibirla
// (handleAssetChunk): no decodificable o fuera de los topes de imagefile.
func (a *App) imageSendable(data []byte) bool {
	err := imagefile.Check(data)
	switch {
	case err == nil:
		return true
	case errors.Is(err, imagefile.ErrTooLarge):
		a.setStatus(fmt.Sprintf("La imagen es demasiado grande (máximo %d MP y %d px por lado).", imagefile.MaxPixels/1_000_000, imagefile.MaxSide))
	default:
		a.setStatus(unsupportedImageStatus)
	}
	logging.Infof("imagen no enviada: %s", logging.FormatError(err))
	return false
}

func (a *App) sendChatImageBytes(ext string, data []byte, caption string) {
	if a.historyStore == nil || !a.connected() {
		return
	}
	imageID := history.NewID(a.participantID)
	if ext == "" {
		ext = ".png"
	}
	ext = strings.ToLower(ext)
	if history.CheckImage(imageID, ext) != nil {
		a.setStatus(unsupportedImageStatus)
		return
	}
	if !a.imageSendable(data) {
		return
	}
	if _, saveErr := a.historyStore.SaveImage(imageID, ext, data); saveErr != nil {
		logging.Errorf("guardar imagen %q: %s", imageID, logging.FormatError(saveErr))
		a.setStatus("No se pudo guardar la imagen: " + saveErr.Error())
		return
	}
	message := history.Message{
		ID:        history.NewID(a.participantID),
		Timestamp: time.Now().UTC(),
		SenderID:  a.participantID,
		Username:  a.conf().Username,
		Kind:      "image",
		ImageID:   imageID,
		ImageExt:  ext,
		Text:      caption,
	}
	a.addChatMessage(message)
	a.playEventSound(audio.SFXMessage)
	payload, encodeErr := room.Encode(room.Envelope{Kind: room.ChatMessage, Message: &message})
	if encodeErr == nil {
		a.sendChatPayload(payload)
	}
	a.sendAsset(assets.KindImage, imageID, ext, data)
}

// maxActivityEntries limita el registro de actividad: cada label con wrapping se re-mide en cada
// layout, así que un historial largo hacía cada Add más lento que el anterior.
const maxActivityEntries = 200

type activityEntry struct {
	text string
	at   time.Time
}

func newActivityLabel(text string, t time.Time) *widget.Label {
	var stamp string
	now := time.Now()
	localT := t.Local()

	// Si es de hoy, muestra solo la hora. Si es historial viejo, muestra fecha y hora.
	if localT.Year() == now.Year() && localT.YearDay() == now.YearDay() {
		stamp = localT.Format("15:04")
	} else {
		stamp = localT.Format("02/01 15:04")
	}
	lbl := widget.NewLabel(fmt.Sprintf("%s %s", stamp, text))
	lbl.Wrapping = fyne.TextWrapWord
	return lbl
}

func (a *App) addActivityAt(text string, t time.Time) {
	a.addActivityBatch([]activityEntry{{text: text, at: t}})
}

// addActivityBatch agrega varias entradas con un solo fyne.Do (un Refresh y un ScrollToBottom).
// initHistory antes encolaba un fyne.Do por mensaje de sistema y congelaba la UI al conectar.
func (a *App) addActivityBatch(entries []activityEntry) {
	if len(entries) == 0 {
		return
	}
	if len(entries) > maxActivityEntries {
		entries = entries[len(entries)-maxActivityEntries:]
	}
	labels := make([]fyne.CanvasObject, 0, len(entries))
	for _, entry := range entries {
		labels = append(labels, newActivityLabel(entry.text, entry.at))
	}
	fyne.Do(func() {
		if a.activityBox == nil {
			return
		}
		objects := append(a.activityBox.Objects, labels...)
		if len(objects) > maxActivityEntries {
			objects = append([]fyne.CanvasObject(nil), objects[len(objects)-maxActivityEntries:]...)
		}
		a.activityBox.Objects = objects
		a.activityBox.Refresh()
		if a.activityScroll != nil {
			a.activityScroll.ScrollToBottom()
		}
	})
}

// Wrapper para mantener compatibilidad con todos los logs en tiempo real
func (a *App) addActivity(text string) {
	a.addActivityAt(text, time.Now())
}

func buildChatSegments(m history.Message, userColor fyne.ThemeColorName) []widget.RichTextSegment {
	stamp := m.Timestamp.Local().Format("15:04")
	prefix := m.Username

	stampStyle := widget.RichTextStyleInline
	stampStyle.ColorName = theme.ColorNamePlaceHolder

	userStyle := widget.RichTextStyleInline
	userStyle.TextStyle = fyne.TextStyle{Bold: true}
	userStyle.ColorName = userColor

	segments := []widget.RichTextSegment{
		&widget.TextSegment{
			Text:  stamp + " ",
			Style: stampStyle,
		},
		&widget.TextSegment{
			Text:  prefix + ": ",
			Style: userStyle,
		},
	}

	text := m.Text
	if m.Kind == "image" && text == "" {
		text = "📷 Imagen compartida"
	}

	matches := urlRegex.FindAllStringIndex(text, -1)
	if len(matches) == 0 {
		segments = append(segments, &widget.TextSegment{
			Text:  text,
			Style: widget.RichTextStyleInline,
		})
		return segments
	}

	lastIdx := 0
	for _, loc := range matches {
		start, end := loc[0], loc[1]
		if start > lastIdx {
			segments = append(segments, &widget.TextSegment{
				Text:  text[lastIdx:start],
				Style: widget.RichTextStyleInline,
			})
		}
		rawURL := text[start:end]
		parsedURL, err := url.Parse(rawURL)
		if err == nil {
			segments = append(segments, &widget.HyperlinkSegment{
				Text: rawURL,
				URL:  parsedURL,
			})
		} else {
			segments = append(segments, &widget.TextSegment{
				Text:  rawURL,
				Style: widget.RichTextStyleInline,
			})
		}
		lastIdx = end
	}
	if lastIdx < len(text) {
		segments = append(segments, &widget.TextSegment{
			Text:  text[lastIdx:],
			Style: widget.RichTextStyleInline,
		})
	}
	return segments
}

func (a *App) buildChatMessageObject(m history.Message, isMatch bool) fyne.CanvasObject {
	var userColor fyne.ThemeColorName
	if m.SenderID == a.participantID || (m.Username != "" && m.Username == a.conf().Username) {
		userColor = theme.ColorNameSuccess
	} else {
		userColor = theme.ColorNamePrimary
	}

	segments := buildChatSegments(m, userColor)
	rt := widget.NewRichText(segments...)
	rt.Wrapping = fyne.TextWrapWord

	copyText := m.Text
	if copyText == "" && m.Kind == "image" {
		copyText = "Imagen: " + m.ImageID + m.ImageExt
	}
	copyBtn := widget.NewButtonWithIcon("", theme.ContentCopyIcon(), func() {
		_ = clipboard.WriteAll(copyText)
		a.setStatus("Mensaje copiado al portapapeles.")
	})
	copyBtn.Importance = widget.LowImportance
	copyWrap := container.NewGridWrap(fyne.NewSize(28, 24), copyBtn)

	headerRow := container.NewBorder(nil, nil, nil, copyWrap, rt)
	var row fyne.CanvasObject = headerRow

	imagePath := ""
	if m.Kind == "image" && a.historyStore != nil {
		// Con un ID o extensión inválidos (mensaje de red) queda la fila sin imagen.
		imagePath, _ = a.historyStore.ImagePath(m.ImageID, m.ImageExt)
	}
	if imagePath != "" && fileExists(imagePath) {
		img := newChatThumbnail(imagePath)
		p := imagePath
		clickableThumb := newTappableImage(img, func() {
			a.showLightbox(p)
		})
		row = container.NewVBox(headerRow, container.NewHBox(clickableThumb))
	}

	if isMatch {
		highlightBg := canvas.NewRectangle(color.NRGBA{R: 70, G: 65, B: 30, A: 255})
		highlightBg.CornerRadius = 4
		row = container.NewStack(highlightBg, container.NewPadded(row))
	}

	return row
}

func (a *App) refreshChat() {
	fyne.Do(func() {
		if a.chatBox == nil {
			return
		}
		a.chatMu.RLock()
		msgs := append([]history.Message(nil), a.chatMessages...)
		a.chatMu.RUnlock()

		var searchFilter string
		if a.chatSearch != nil {
			searchFilter = strings.ToLower(strings.TrimSpace(a.chatSearch.Text))
		}

		matchCount := 0
		objects := make([]fyne.CanvasObject, 0, len(msgs))
		for _, msg := range msgs {
			m := msg
			if m.System || m.Kind == "system" || strings.EqualFold(m.Username, "SISTEMA") {
				continue
			}
			isMatch := false
			if searchFilter != "" {
				lowerText := strings.ToLower(m.Text)
				lowerUser := strings.ToLower(m.Username)
				if strings.Contains(lowerText, searchFilter) || strings.Contains(lowerUser, searchFilter) {
					isMatch = true
					matchCount++
				}
			}

			objects = append(objects, a.buildChatMessageObject(m, isMatch))
		}

		if a.chatSearchCount != nil {
			if searchFilter == "" {
				a.chatSearchCount.SetText("")
			} else if matchCount == 0 {
				a.chatSearchCount.SetText("(0)")
			} else {
				a.chatSearchCount.SetText(fmt.Sprintf("(%d)", matchCount))
			}
		}

		a.chatBox.Objects = objects
		a.chatBox.Refresh()
		if a.chatScroll != nil && searchFilter == "" {
			a.chatScroll.ScrollToBottom()
		}
	})
}

func (a *App) initHistory(roomName string) {
	root, err := config.AppDir()
	if err != nil {
		return
	}
	store, err := history.New(root, roomName)
	if err != nil {
		return
	}
	a.historyStore = store
	a.activeRoomName = roomName
	a.viewingRoomName = roomName
	messages, err := store.Load()
	if err != nil {
		return
	}
	chatOnly := make([]history.Message, 0, len(messages))
	var activity []activityEntry
	for _, m := range messages {
		if m.System || m.Kind == "system" || strings.EqualFold(m.Username, "SISTEMA") {
			activity = append(activity, activityEntry{text: m.Text, at: m.Timestamp})
			continue
		}
		chatOnly = append(chatOnly, m)
	}
	a.addActivityBatch(activity)
	a.chatMu.Lock()
	a.chatMessages = chatOnly
	a.chatMu.Unlock()
	sounds, _ := store.ListSounds()
	a.soundMu.Lock()
	a.sounds = a.sounds[:0]
	for _, meta := range sounds {
		a.sounds = append(a.sounds, room.Sound{ID: meta.ID, Ext: meta.Ext, Name: meta.Name})
	}
	a.soundMu.Unlock()
	a.rebuildSoundboard()
	a.refreshRoomList(roomName)
	a.refreshChat()
}

func (a *App) addChatMessage(message history.Message) {
	if message.System || message.Kind == "system" || strings.EqualFold(message.Username, "SISTEMA") {
		a.addActivityAt(message.Text, message.Timestamp)
		return
	}
	if a.historyStore != nil {
		go func(m history.Message) {
			_ = a.historyStore.Add(m)
		}(message)
	}
	if a.viewingRoomName != "" && a.activeRoomName != "" && a.viewingRoomName != a.activeRoomName {
		return
	}
	a.chatMu.Lock()
	for _, existing := range a.chatMessages {
		if existing.ID == message.ID {
			a.chatMu.Unlock()
			return
		}
	}
	a.chatMessages = append(a.chatMessages, message)
	a.chatMu.Unlock()

	row := a.buildChatMessageObject(message, false)
	fyne.Do(func() {
		if a.chatBox == nil {
			return
		}
		// El texto del buscador es estado del widget: se lee acá, en el hilo de UI.
		if a.chatSearch != nil && strings.TrimSpace(a.chatSearch.Text) != "" {
			a.refreshChat()
			return
		}
		a.chatBox.Add(row)
		if a.chatScroll != nil {
			a.chatScroll.ScrollToBottom()
		}
	})
}

func (a *App) addSystemMessage(text string) {
	a.addActivity(text)
	if udp := a.transport.Load(); udp != nil && a.roomState.HostID == a.participantID {
		message := history.Message{
			ID:        history.NewID("system"),
			Timestamp: time.Now().UTC(),
			Username:  "SISTEMA",
			Kind:      "system",
			Text:      text,
			System:    true,
		}
		payload, _ := room.Encode(room.Envelope{Kind: room.SystemMessage, Message: &message})
		udp.Broadcast(transport.Packet{Kind: transport.PacketChat, Sequence: a.sequence.Add(1), Payload: payload}, nil)
	}
}

func (a *App) viewRoom(roomName string) {
	roomName = strings.TrimSpace(roomName)
	if roomName == "" {
		return
	}
	a.viewingRoomName = roomName
	root, err := config.AppDir()
	if err != nil {
		return
	}
	store, err := history.New(root, roomName)
	if err != nil {
		a.setStatus("No se pudo abrir el historial de " + roomName)
		return
	}
	messages, _ := store.Load()
	chatOnly := make([]history.Message, 0, len(messages))
	for _, m := range messages {
		if m.System || m.Kind == "system" || strings.EqualFold(m.Username, "SISTEMA") {
			continue
		}
		chatOnly = append(chatOnly, m)
	}
	a.chatMu.Lock()
	a.chatMessages = chatOnly
	a.chatMu.Unlock()
	a.refreshChat()
}

func (a *App) refreshRoomList(selected string) {
	root, err := config.AppDir()
	if err != nil {
		return
	}
	rooms, err := history.ListRooms(root)
	if err != nil {
		return
	}
	found := false
	for _, name := range rooms {
		if name == selected {
			found = true
			break
		}
	}
	if selected != "" && !found {
		rooms = append(rooms, selected)
		sort.Strings(rooms)
	}
	fyne.Do(func() {
		if a.roomSelect == nil {
			return
		}
		a.roomSelect.Options = rooms
		a.roomSelect.Refresh()
		if selected != "" {
			a.roomSelect.SetSelected(selected)
		}
	})
}

func (a *App) selectChatImage() {
	if a.historyStore == nil || !a.connected() {
		a.setStatus("Conecta a una sala antes de enviar una imagen.")
		return
	}
	// El HWND se lee en el hilo de UI; el diálogo nativo corre en su propia goroutine (ver selectFile).
	owner := a.dialogOwner()
	go func() {
		defer func() {
			if value := recover(); value != nil {
				logging.Panic(value)
				a.setStatus("No se pudo abrir el selector de imágenes.")
			}
		}()
		path, err := selectImageFile(owner)
		if err != nil {
			if err.Error() != "selección cancelada" {
				logging.Errorf("selector de imágenes: %s", logging.FormatError(err))
			}
			return
		}
		a.sendImageFile(path)
	}()
}

func (a *App) sendImageFile(path string) {
	if a.historyStore == nil || !a.connected() {
		return
	}
	// El selector permite "Todos los archivos": se rechaza antes de leerlo.
	imageID := history.NewID(a.participantID)
	extension := strings.ToLower(filepath.Ext(path))
	if history.CheckImage(imageID, extension) != nil {
		a.setStatus(unsupportedImageStatus)
		return
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		logging.Errorf("leer imagen %q: %s", path, logging.FormatError(readErr))
		a.setStatus("No se pudo leer la imagen: " + readErr.Error())
		return
	}
	if len(data) > assets.MaxBytes {
		a.setStatus(fmt.Sprintf("El archivo supera el límite de %d MiB.", assets.MaxBytes/1024/1024))
		return
	}
	if !a.imageSendable(data) {
		return
	}
	if _, saveErr := a.historyStore.SaveImage(imageID, extension, data); saveErr != nil {
		logging.Errorf("guardar imagen %q: %s", imageID, logging.FormatError(saveErr))
		a.setStatus("No se pudo guardar la imagen: " + saveErr.Error())
		return
	}
	message := history.Message{ID: history.NewID(a.participantID), Timestamp: time.Now().UTC(), SenderID: a.participantID, Username: a.conf().Username, Kind: "image", ImageID: imageID, ImageExt: extension}
	a.addChatMessage(message)
	a.playEventSound(audio.SFXMessage)
	payload, encodeErr := room.Encode(room.Envelope{Kind: room.ChatMessage, Message: &message})
	if encodeErr == nil {
		a.sendChatPayload(payload)
	}
	a.sendAsset(assets.KindImage, imageID, extension, data)
}
