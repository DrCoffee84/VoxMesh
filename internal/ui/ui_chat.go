//go:build cgo

package ui

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/atotto/clipboard"

	"voxmesh/internal/assets"
	"voxmesh/internal/audio"
	"voxmesh/internal/config"
	"voxmesh/internal/history"
	"voxmesh/internal/i18n"
	"voxmesh/internal/imagefile"
	"voxmesh/internal/logging"
	"voxmesh/internal/room"
	"voxmesh/internal/transport"
)

var urlRegex = regexp.MustCompile(`https?://[^\s<>"'()]+`)

// isSystemMessage: los avisos de la sala ("X se conectó", "X será el nuevo host"…). Van al registro
// de actividad, que es solo de la sesión: no se guardan, no se cargan del historial ni viajan en la
// sincronización. Los que quedaron en historiales de antes del 29/09 se ignoran.
func isSystemMessage(m history.Message) bool {
	return m.System || m.Kind == "system" || strings.EqualFold(m.Username, "SISTEMA")
}

func (a *App) stageImage(ext string, data []byte, name string) {
	if len(data) > assets.MaxBytes {
		a.setStatus(i18n.M("chat.image_over_limit", assets.MaxBytes/1024/1024))
		return
	}
	a.stagedExt = ext
	a.stagedData = data
	a.stagedName = name
	kb := len(data) / 1024
	fyne.Do(func() {
		if a.stagedLabel != nil {
			a.stagedLabel.SetText(i18n.T("chat.image_staged_label", name, kb))
		}
		if a.stagedBar != nil {
			a.stagedBar.Show()
		}
	})
	a.setStatus(i18n.M("chat.image_staged", name, kb))
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
		a.setStatus(i18n.M("chat.connect_first_message"))
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

const unsupportedImageStatus = "chat.image_unsupported"

// imageSendable rechaza antes de mandarla una imagen que los demás tirarían al recibirla
// (handleAssetChunk): no decodificable o fuera de los topes de imagefile.
func (a *App) imageSendable(data []byte) bool {
	err := imagefile.Check(data)
	switch {
	case err == nil:
		return true
	case errors.Is(err, imagefile.ErrTooLarge):
		a.setStatus(i18n.M("chat.image_too_large", imagefile.MaxPixels/1_000_000, imagefile.MaxSide))
	default:
		a.setStatus(i18n.M(unsupportedImageStatus))
	}
	logging.Infof("imagen no enviada: %s", logging.FormatError(err))
	return false
}

func (a *App) sendChatImageBytes(ext string, data []byte, caption string) {
	store := a.historyStore.Load()
	if store == nil || !a.connected() {
		return
	}
	imageID := history.NewID(a.participantID)
	if ext == "" {
		ext = ".png"
	}
	ext = strings.ToLower(ext)
	if history.CheckImage(imageID, ext) != nil {
		a.setStatus(i18n.M(unsupportedImageStatus))
		return
	}
	if !a.imageSendable(data) {
		return
	}
	if _, saveErr := store.SaveImage(imageID, ext, data); saveErr != nil {
		logging.Errorf("guardar imagen %q: %s", imageID, logging.FormatError(saveErr))
		a.setStatus(i18n.M("chat.image_save_failed", saveErr.Error()))
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

// newActivityRow es una fila del registro: la hora en gris y el texto, con salto de línea.
func newActivityRow(text string, t time.Time) *widget.RichText {
	var stamp string
	now := time.Now()
	localT := t.Local()

	// Si es de hoy, muestra solo la hora. Si es historial viejo, muestra fecha y hora.
	if localT.Year() == now.Year() && localT.YearDay() == now.YearDay() {
		stamp = localT.Format("15:04")
	} else {
		stamp = localT.Format("02/01 15:04")
	}
	stampStyle := widget.RichTextStyleInline
	stampStyle.ColorName = theme.ColorNamePlaceHolder
	row := widget.NewRichText(
		&widget.TextSegment{Text: stamp + " ", Style: stampStyle},
		&widget.TextSegment{Text: text, Style: widget.RichTextStyleInline},
	)
	row.Wrapping = fyne.TextWrapWord
	return row
}

func (a *App) addActivityAt(text string, t time.Time) {
	a.addActivityBatch([]activityEntry{{text: text, at: t}})
}

// addActivityBatch agrega varias entradas con un solo fyne.Do (un Refresh y un ScrollToBottom).
func (a *App) addActivityBatch(entries []activityEntry) {
	if len(entries) == 0 {
		return
	}
	if len(entries) > maxActivityEntries {
		entries = entries[len(entries)-maxActivityEntries:]
	}
	rows := make([]fyne.CanvasObject, 0, len(entries))
	for _, entry := range entries {
		rows = append(rows, newActivityRow(entry.text, entry.at))
	}
	fyne.Do(func() {
		if a.activityBox == nil {
			return
		}
		objects := append(a.activityBox.Objects, rows...)
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
	stamp := m.Timestamp.Local().Format(chatStampFormat)
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
		text = i18n.T("chat.image_shared")
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
				Text: shortLink(rawURL),
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

// maxLinkRunes es lo más largo que se muestra de un link en el chat.
const maxLinkRunes = 40

// shortLink acorta el texto visible de un link largo: Fyne no parte un HyperlinkSegment al hacer
// el salto de línea y se salía de la fila. El link abre la URL entera y "copiar" copia el mensaje.
func shortLink(link string) string {
	if runes := []rune(link); len(runes) > maxLinkRunes {
		return string(runes[:maxLinkRunes-1]) + "…"
	}
	return link
}

func (a *App) buildChatMessageObject(m history.Message, isMatch bool) fyne.CanvasObject {
	row, _ := a.buildChatRow(m, isMatch, false)
	return row
}

// buildChatRow arma la fila de m: resaltada si coincide con lo buscado y, si es la coincidencia
// actual (C40), con el borde. Devuelve también las partes que cambian con el buscador.
func (a *App) buildChatRow(m history.Message, isMatch, isCurrent bool) (fyne.CanvasObject, chatRowMarks) {
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
		copyText = i18n.T("chat.copy_image", m.ImageID+m.ImageExt)
	}
	// El renglón se ilumina mientras el mouse está sobre su ícono de copiar y destella al copiar
	// (C29): con renglones cortos el ícono queda lejos del texto.
	highlight := newRowHighlight()
	copyBtn := newCopyButton(highlight, func() {
		_ = clipboard.WriteAll(copyText)
		highlight.flash()
		a.setStatus(i18n.M("chat.copied"))
	})
	copyWrap := container.NewGridWrap(fyne.NewSize(28, 24), copyBtn)

	// La hora con su leyenda (C39), encima de la que dibuja rt.
	text := container.New(stampLayout{}, rt, a.newChatStamp(m.Timestamp))
	headerRow := container.NewBorder(nil, nil, nil, copyWrap, text)
	var row fyne.CanvasObject = headerRow

	if imagePath := a.chatImagePath(m); imagePath != "" && fileExists(imagePath) {
		img := newChatThumbnail(imagePath)
		p := imagePath
		clickableThumb := newTappableImage(img, func() {
			a.showLightbox(p)
		})
		row = container.NewVBox(headerRow, container.NewHBox(clickableThumb))
	}

	marks := chatRowMarks{highlight: highlight}
	if isMatch {
		marks.match = newThemedRect(colorNameChatMatch, 4)
		marks.setCurrent(isCurrent)
		return container.NewStack(marks.match, highlight.rect, container.NewPadded(row)), marks
	}
	return container.NewStack(highlight.rect, row), marks
}

// chatImagePath es dónde está la imagen de m ("" si no es una imagen o no hay sala). Está en la
// carpeta de la sala que muestra el chat, que puede no ser en la que estoy ("Ver otra sala…") o no
// haber ninguna (fuera de la sala). Con un ID o extensión inválidos (mensaje de red) da "".
func (a *App) chatImagePath(m history.Message) string {
	store := a.viewStore.Load()
	if m.Kind != "image" || store == nil {
		return ""
	}
	path, _ := store.ImagePath(m.ImageID, m.ImageExt)
	return path
}

// maxChatRows es cuántos mensajes arma el chat (C38): Fyne recorre todas las filas en cada
// repintado aunque estén fuera de la vista, y cada imagen es una miniatura de ~0,6 MB en memoria.
// Los anteriores se arman de a maxChatRows con "Ver anteriores". Entre 10 y 20 no cambia nada: lo
// que pesa son las imágenes.
const maxChatRows = 20

// chatVisibleMessages es una copia de los mensajes del chat sin los avisos de la sala.
func (a *App) chatVisibleMessages() []history.Message {
	a.chatMu.RLock()
	defer a.chatMu.RUnlock()
	visible := make([]history.Message, 0, len(a.chatMessages))
	for _, m := range a.chatMessages {
		if !isSystemMessage(m) {
			visible = append(visible, m)
		}
	}
	return visible
}

// chatSearchText es lo buscado, en minúsculas ("" sin buscador o vacío). Hilo de UI.
func (a *App) chatSearchText() string {
	if a.chatSearch == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(a.chatSearch.Text))
}

func (a *App) refreshChat() {
	fyne.Do(a.renderChat)
}

// reloadChat vuelve a los últimos maxChatRows y suelta las miniaturas: al cambiar de sala y al
// colgar (C38). También saca el rango de fechas y la coincidencia actual del buscador (C40).
func (a *App) reloadChat() {
	fyne.Do(func() {
		a.chatLimit = maxChatRows
		thumbCache.clear()
		a.clearChatSearchState()
		a.renderChat()
	})
}

// renderChat rearma las filas del chat (hilo de UI): las últimas chatLimit y, con algo buscado, por
// lo menos hasta la coincidencia actual (C38: el buscador busca en todo). Las coincidencias van
// resaltadas, sin esconder el resto, y la actual con un borde; el contador dice "actual/total".
func (a *App) renderChat() {
	if a.chatBox == nil {
		return
	}
	a.chatLimit = max(a.chatLimit, maxChatRows)
	visible := a.chatVisibleMessages()
	filter := a.chatFilter()
	indexes := chatMatchIndexes(visible, filter)
	current := a.chatCurrentPos(visible, indexes)

	from := max(0, len(visible)-a.chatLimit)
	if current >= 0 {
		from = min(from, indexes[current])
	}
	shown := visible[from:]
	objects := make([]fyne.CanvasObject, 0, len(shown))
	marks := make([]chatRowMarks, 0, len(shown))
	for _, m := range shown {
		row, rowMarks := a.buildChatRow(m, filter.matches(m), m.ID == a.chatCurrentID)
		objects = append(objects, row)
		marks = append(marks, rowMarks)
	}
	a.setChatCounter(filter.active(), current, len(indexes))

	// Fuera de una sala y sin mirar otra (al abrir la app y después de colgar, C31): en vez
	// del chat vacío, cómo se entra.
	if a.viewingRoomName == "" {
		objects = []fyne.CanvasObject{noRoomHint()}
		shown, marks, from = nil, nil, 0
	}

	releaseChatImages(a.chatBox.Objects)
	a.chatShown, a.chatMarks = shown, marks
	a.keepChatThumbs()
	a.setChatMore(from)
	a.chatBox.Objects = objects
	a.chatBox.Refresh()
	if a.chatScroll != nil && !filter.active() {
		a.chatScroll.ScrollToBottom()
	}
}

// appendChatRows suma al final las filas de los mensajes que llegaron después de la última armada y
// saca del principio las que pasan de chatLimit (hilo de UI). Sin buscar: con algo buscado, o si
// las filas no son las del final del chat, renderChat.
func (a *App) appendChatRows() {
	if a.chatBox == nil {
		return
	}
	visible := a.chatVisibleMessages()
	last := -1
	if n := len(a.chatShown); n > 0 {
		for i := len(visible) - 1; i >= 0; i-- {
			if visible[i].ID == a.chatShown[n-1].ID {
				last = i
				break
			}
		}
	}
	if last < 0 || a.chatFilter().active() || a.viewingRoomName == "" {
		a.renderChat()
		return
	}
	added := visible[last+1:]
	if len(added) == 0 {
		return
	}
	objects := append([]fyne.CanvasObject(nil), a.chatBox.Objects...)
	shown := append(append([]history.Message(nil), a.chatShown...), added...)
	marks := append([]chatRowMarks(nil), a.chatMarks...)
	for _, m := range added {
		row, rowMarks := a.buildChatRow(m, false, false)
		objects = append(objects, row)
		marks = append(marks, rowMarks)
	}
	if over := len(shown) - a.chatLimit; over > 0 {
		releaseChatImages(objects[:over])
		// Copias nuevas: con objects[over:] el arreglo de atrás seguiría apuntando a las filas viejas.
		objects = append([]fyne.CanvasObject(nil), objects[over:]...)
		shown = append([]history.Message(nil), shown[over:]...)
		marks = append([]chatRowMarks(nil), marks[over:]...)
	}
	a.chatShown, a.chatMarks = shown, marks
	a.keepChatThumbs()
	a.setChatMore(len(visible) - len(shown))
	a.chatBox.Objects = objects
	a.chatBox.Refresh()
	if a.chatScroll != nil {
		a.chatScroll.ScrollToBottom()
	}
}

// keepChatThumbs deja en thumbCache solo las miniaturas de las filas armadas.
func (a *App) keepChatThumbs() {
	paths := map[string]bool{}
	for _, m := range a.chatShown {
		if path := a.chatImagePath(m); path != "" {
			paths[path] = true
		}
	}
	thumbCache.keep(paths)
}

// setChatMore muestra "Ver anteriores (N)" arriba del chat si quedaron N mensajes sin armar.
func (a *App) setChatMore(hidden int) {
	if a.chatMore == nil {
		return
	}
	if hidden <= 0 {
		a.chatMore.Hide()
		return
	}
	a.chatMore.SetText(i18n.T("chat.show_older", hidden))
	a.chatMore.Show()
}

// showOlderChat ("Ver anteriores") arma maxChatRows mensajes más y deja a la vista lo mismo que
// antes: las filas nuevas quedan arriba, para subir a verlas.
func (a *App) showOlderChat() {
	content := a.chatScroll.Content
	before, offset := content.MinSize().Height, a.chatScroll.Offset.Y
	a.chatLimit += maxChatRows
	a.renderChat()
	a.scrollChatTo(offset + content.MinSize().Height - before)
}

// scrollChatTo deja el scroll del chat en y. Lo acomoda primero: el contenido recién rearmado
// todavía tiene el tamaño viejo y ScrollToOffset lo usa para el tope.
func (a *App) scrollChatTo(y float32) {
	a.chatScroll.Refresh()
	a.chatScroll.ScrollToOffset(fyne.NewPos(0, y))
}

// trimChatForTray vuelve a las últimas maxChatRows filas al mandar la app a la bandeja (C38), así
// se suelta lo que se había armado con "Ver anteriores" o el buscador. Hilo de UI.
func (a *App) trimChatForTray() {
	a.chatLimit = maxChatRows
	if len(a.chatShown) > maxChatRows {
		a.renderChat()
	}
}

// noRoomHint es el texto del chat fuera de una sala: dónde están las salas guardadas y cómo se
// entra a la de otro.
func noRoomHint() *widget.Label {
	hint := widget.NewLabel(i18n.T("chat.no_room"))
	hint.Wrapping = fyne.TextWrapWord
	hint.Importance = widget.LowImportance
	return hint
}

// closeRoomView deja la ventana como al abrir la app (C31): sin sala, sin chat, sin botonera y
// con "Ver otra sala…" sin elegir. La llama stopConnectionAsync al colgar y deleteSavedRoom
// antes de borrar la sala que muestra el chat.
func (a *App) closeRoomView() {
	a.historyStore.Store(nil)
	a.viewStore.Store(nil)
	a.viewingRoomName = ""
	a.chatMu.Lock()
	a.chatMessages = nil
	a.chatMu.Unlock()
	a.soundMu.Lock()
	a.sounds = nil
	a.soundMu.Unlock()
	a.rebuildSoundboard()
	a.refreshRoomList("")
	a.reloadChat()
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
	a.historyStore.Store(store)
	a.viewStore.Store(store)
	a.activeRoomName = roomName
	a.viewingRoomName = roomName
	messages, err := store.Load()
	if err != nil {
		return
	}
	// Solo el chat: el registro de actividad es de la sesión (isSystemMessage). Antes se cargaban
	// los avisos viejos del archivo y quedaban al final del registro, debajo de los de hoy.
	chatOnly := make([]history.Message, 0, len(messages))
	for _, m := range messages {
		if !isSystemMessage(m) {
			chatOnly = append(chatOnly, m)
		}
	}
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
	a.reloadChat()
}

func (a *App) addChatMessage(message history.Message) {
	// Los avisos en vivo van directo al registro (handleChatPacket); acá solo llegan los viejos de
	// la sincronización del historial, que se ignoran (isSystemMessage).
	if isSystemMessage(message) {
		return
	}
	if store := a.historyStore.Load(); store != nil {
		go func(m history.Message) {
			_ = store.Add(m)
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

	// La fila se arma en el hilo de UI, junto con el recorte de las viejas (C38).
	fyne.Do(a.appendChatRows)
}

// addSystemMessage anota un aviso de la sala (key: "chat.sys.*") en el registro de actividad y,
// si soy el host, lo difunde.
func (a *App) addSystemMessage(key string, args ...string) {
	message := newSystemMessage(key, args...)
	a.addActivity(message.Text)
	if udp := a.transport.Load(); udp != nil && a.roomState.HostID == a.participantID {
		payload, _ := room.Encode(room.Envelope{Kind: room.SystemMessage, Message: &message})
		udp.Broadcast(transport.Packet{Kind: transport.PacketChat, Sequence: a.sequence.Add(1), Payload: payload}, nil)
	}
}

// newSystemMessage arma un aviso de la sala. Viaja como clave + datos para que cada uno lo lea en
// su idioma (systemMessageText); Text lleva el aviso en el mío, de respaldo.
func newSystemMessage(key string, args ...string) history.Message {
	return history.Message{
		ID:        history.NewID("system"),
		Timestamp: time.Now().UTC(),
		Username:  "SISTEMA",
		Kind:      "system",
		Text:      i18n.T(key, stringArgs(args)...),
		System:    true,
		Key:       key,
		Args:      args,
	}
}

// systemMessageText es el texto de un aviso recibido: en mi idioma si trae una clave de aviso que
// conozco; si no, el Text que mandó el host.
func systemMessageText(m history.Message) string {
	if strings.HasPrefix(m.Key, "chat.sys.") && i18n.Has(m.Key) {
		return i18n.T(m.Key, stringArgs(m.Args)...)
	}
	return m.Text
}

func stringArgs(args []string) []any {
	values := make([]any, len(args))
	for i, arg := range args {
		values[i] = arg
	}
	return values
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
		a.setStatus(i18n.M("chat.history_open_failed", roomName))
		return
	}
	a.viewStore.Store(store)
	messages, _ := store.Load()
	chatOnly := make([]history.Message, 0, len(messages))
	for _, m := range messages {
		if isSystemMessage(m) {
			continue
		}
		chatOnly = append(chatOnly, m)
	}
	a.chatMu.Lock()
	a.chatMessages = chatOnly
	a.chatMu.Unlock()
	a.reloadChat()
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
		} else if a.roomSelect.Selected != "" {
			a.roomSelect.ClearSelected() // su OnChanged("") no abre nada (viewRoom)
		}
	})
}

func (a *App) selectChatImage() {
	if a.historyStore.Load() == nil || !a.connected() {
		a.setStatus(i18n.M("chat.connect_first_image"))
		return
	}
	// El HWND se lee en el hilo de UI; el diálogo nativo corre en su propia goroutine (ver selectFile).
	owner := a.dialogOwner()
	go func() {
		defer func() {
			if value := recover(); value != nil {
				logging.Panic(value)
				a.setStatus(i18n.M("chat.image_picker_failed"))
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
	store := a.historyStore.Load()
	if store == nil || !a.connected() {
		return
	}
	// El selector permite "Todos los archivos": se rechaza antes de leerlo.
	imageID := history.NewID(a.participantID)
	extension := strings.ToLower(filepath.Ext(path))
	if history.CheckImage(imageID, extension) != nil {
		a.setStatus(i18n.M(unsupportedImageStatus))
		return
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		logging.Errorf("leer imagen %q: %s", path, logging.FormatError(readErr))
		a.setStatus(i18n.M("chat.image_read_failed", readErr.Error()))
		return
	}
	if len(data) > assets.MaxBytes {
		a.setStatus(i18n.M("chat.file_over_limit", assets.MaxBytes/1024/1024))
		return
	}
	if !a.imageSendable(data) {
		return
	}
	if _, saveErr := store.SaveImage(imageID, extension, data); saveErr != nil {
		logging.Errorf("guardar imagen %q: %s", imageID, logging.FormatError(saveErr))
		a.setStatus(i18n.M("chat.image_save_failed", saveErr.Error()))
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
