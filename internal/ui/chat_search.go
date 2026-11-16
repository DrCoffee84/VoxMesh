//go:build cgo

package ui

import (
	"fmt"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"voxmesh/internal/history"
	"voxmesh/internal/i18n"
)

// Buscador del chat (AGENDA C40): flechas para recorrer las coincidencias, la actual marcada, y un
// rango de fechas que se combina con el texto. El rango y la actual son de la ventana: no van a la
// config y se limpian al cambiar de sala o al colgar (reloadChat).

// chatFilter es lo que busca el buscador del chat.
type chatFilter struct {
	text string // en minúsculas; "" = sin texto
	// from es el comienzo del día "Desde" y to el del día después de "Hasta" (excluido), en hora
	// local; cero = abierto de ese lado.
	from, to time.Time
}

// active dice si hay algo buscado (texto o rango).
func (f chatFilter) active() bool {
	return f.text != "" || !f.from.IsZero() || !f.to.IsZero()
}

// matches dice si m cumple las dos condiciones: el texto (en el mensaje o en el nombre) y el rango.
// Con el texto vacío y un rango, coinciden todos los mensajes del rango.
func (f chatFilter) matches(m history.Message) bool {
	if !f.active() {
		return false
	}
	if f.text != "" && !strings.Contains(strings.ToLower(m.Text), f.text) && !strings.Contains(strings.ToLower(m.Username), f.text) {
		return false
	}
	if !f.from.IsZero() && m.Timestamp.Before(f.from) {
		return false
	}
	return f.to.IsZero() || m.Timestamp.Before(f.to)
}

// chatMatchIndexes son los índices de visible que coinciden con f, del más viejo al más nuevo.
func chatMatchIndexes(visible []history.Message, f chatFilter) []int {
	var indexes []int
	for i, m := range visible {
		if f.matches(m) {
			indexes = append(indexes, i)
		}
	}
	return indexes
}

// dayStart es el comienzo del día de d (su año, mes y día, sea cual sea su zona) en hora local:
// DateEntry da la fecha elegida a las 00:00 UTC.
func dayStart(d time.Time) time.Time {
	year, month, day := d.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.Local)
}

// rangeFilter arma los límites del filtro con los días elegidos (nil = abierto).
func rangeFilter(text string, from, to *time.Time) chatFilter {
	f := chatFilter{text: text}
	if from != nil {
		f.from = dayStart(*from)
	}
	if to != nil {
		f.to = dayStart(*to).AddDate(0, 0, 1)
	}
	return f
}

// matchMove es una flecha del buscador.
type matchMove int

const (
	matchFirst matchMove = iota // ⏮ el más viejo
	matchPrev                   // ▲ el anterior (más viejo); también Enter
	matchNext                   // ▼ el siguiente (más nuevo)
	matchLast                   // ⏭ el más nuevo
)

// moveMatch mueve la coincidencia actual (cur, de 0 = la más vieja a n-1) sin dar la vuelta, y dice
// si quedó en una punta (la primera o la última): ahí el renglón destella, también si ya estaba.
func moveMatch(cur, n int, move matchMove) (pos int, edge bool) {
	switch move {
	case matchFirst:
		pos = 0
	case matchPrev:
		pos = cur - 1
	case matchNext:
		pos = cur + 1
	default:
		pos = n - 1
	}
	pos = min(max(pos, 0), n-1)
	return pos, pos == 0 || pos == n-1
}

// chatRowMarks son las partes de una fila que cambian con el buscador: el fondo de coincidencia
// (nil si no coincide) y el del renglón que destella.
type chatRowMarks struct {
	match     *themedRect
	highlight *rowHighlight
}

// setCurrent marca la fila como la coincidencia actual (con el borde del color de resaltado).
func (r chatRowMarks) setCurrent(current bool) {
	if r.match == nil {
		return
	}
	stroke, width := fyne.ThemeColorName(""), float32(0)
	if current {
		stroke, width = theme.ColorNamePrimary, 2
	}
	if r.match.stroke != stroke {
		r.match.stroke, r.match.strokeWidth = stroke, width
		r.match.Refresh()
	}
}

// chatFilter es lo buscado ahora (hilo de UI).
func (a *App) chatFilter() chatFilter {
	return rangeFilter(a.chatSearchText(), a.chatRangeFrom, a.chatRangeTo)
}

// chatCurrentPos es la posición en indexes de la coincidencia actual: la de chatCurrentID o, si no
// está (recién escrito, o ya no coincide), la más nueva. -1 sin coincidencias.
func (a *App) chatCurrentPos(visible []history.Message, indexes []int) int {
	if len(indexes) == 0 {
		a.chatCurrentID = ""
		return -1
	}
	for pos, i := range indexes {
		if visible[i].ID == a.chatCurrentID {
			return pos
		}
	}
	a.chatCurrentID = visible[indexes[len(indexes)-1]].ID
	return len(indexes) - 1
}

// setChatCounter pone "actual/total" al lado del buscador (vacío sin buscar) y muestra las flechas
// solo con algo buscado.
func (a *App) setChatCounter(active bool, pos, total int) {
	if a.chatSearchCount != nil {
		text := ""
		if active {
			text = fmt.Sprintf("%d/%d", pos+1, total)
		}
		a.chatSearchCount.SetText(text)
	}
	if a.chatNav != nil {
		if active {
			a.chatNav.Show()
		} else {
			a.chatNav.Hide()
		}
	}
}

// chatRowOf es la fila armada del mensaje id (-1 si no está armada).
func (a *App) chatRowOf(id string) int {
	for i, m := range a.chatShown {
		if m.ID == id {
			return i
		}
	}
	return -1
}

// chatSearchChanged: cambió el texto o el rango. La actual pasa a ser la más nueva y el scroll la
// trae; sin nada buscado, el chat vuelve abajo (renderChat).
func (a *App) chatSearchChanged() {
	a.chatCurrentID = ""
	a.renderChat()
	if row := a.chatRowOf(a.chatCurrentID); row >= 0 && a.chatFilter().active() {
		a.scrollChatToRow(row)
	}
}

// moveChatMatch (las flechas y Enter) va a otra coincidencia: si su fila no estaba armada (C38), se
// arman las filas hasta ella. En la primera o la última, el renglón destella.
func (a *App) moveChatMatch(move matchMove) {
	visible := a.chatVisibleMessages()
	indexes := chatMatchIndexes(visible, a.chatFilter())
	if len(indexes) == 0 || a.chatBox == nil {
		return
	}
	pos, edge := moveMatch(a.chatCurrentPos(visible, indexes), len(indexes), move)
	a.chatCurrentID = visible[indexes[pos]].ID
	row := a.chatRowOf(a.chatCurrentID)
	if row < 0 {
		a.renderChat()
		row = a.chatRowOf(a.chatCurrentID)
	} else {
		for i, marks := range a.chatMarks {
			marks.setCurrent(i == row)
		}
		a.setChatCounter(true, pos, len(indexes))
	}
	if row < 0 {
		return
	}
	a.scrollChatToRow(row)
	if edge {
		a.chatMarks[row].highlight.flash()
	}
}

// scrollChatToRow mueve el scroll lo justo para que se vea la fila (arriba si estaba más arriba,
// abajo si estaba más abajo; si ya se veía, no se mueve).
func (a *App) scrollChatToRow(row int) {
	if a.chatScroll == nil || row < 0 || row >= len(a.chatBox.Objects) {
		return
	}
	a.chatScroll.Refresh() // acomoda las filas recién armadas
	object := a.chatBox.Objects[row]
	top := a.chatBox.Position().Y + object.Position().Y
	bottom := top + object.Size().Height
	offset, view := a.chatScroll.Offset.Y, a.chatScroll.Size().Height
	switch {
	case top < offset:
		offset = top
	case bottom > offset+view:
		offset = bottom - view
	default:
		return
	}
	a.chatScroll.ScrollToOffset(fyne.NewPos(0, offset))
}

// Ícono del rango de fechas (Material "calendar_today"); con un rango puesto, en el color de
// resaltado. Un SVG fijo: theme.CalendarIcon() acá, antes de que exista la app, deja un error en el log.
var (
	calendarResource = fyne.NewStaticResource("calendar_today.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24"><path d="M20 3h-1V1h-2v2H7V1H5v2H4c-1.1 0-2 .9-2 2v16c0 1.1.9 2 2 2h16c1.1 0 2-.9 2-2V5c0-1.1-.9-2-2-2zm0 18H4V8h16v13z"/></svg>`))
	chatRangeIcon    = theme.NewThemedResource(calendarResource)
	chatRangeOnIcon  = theme.NewPrimaryThemedResource(calendarResource)
)

// newChatSearchTools arma el botón del rango de fechas (a la derecha del buscador) y el renglón de
// debajo, solo con algo buscado: el contador "actual/total" y las cuatro flechas. En el mismo
// renglón que el buscador ensanchaban la ventana mínima (TestMainViewIgnoresLongText).
func (a *App) newChatSearchTools() (rangeButton, navRow fyne.CanvasObject) {
	arrows := container.NewHBox(
		a.newHintButton(theme.MediaSkipPreviousIcon(), i18n.T("chat.match_first"), func() { a.moveChatMatch(matchFirst) }),
		a.newHintButton(theme.MoveUpIcon(), i18n.T("chat.match_prev"), func() { a.moveChatMatch(matchPrev) }),
		a.newHintButton(theme.MoveDownIcon(), i18n.T("chat.match_next"), func() { a.moveChatMatch(matchNext) }),
		a.newHintButton(theme.MediaSkipNextIcon(), i18n.T("chat.match_last"), func() { a.moveChatMatch(matchLast) }),
	)
	a.chatNav = container.NewBorder(nil, nil, nil, arrows, a.chatSearchCount)
	a.chatNav.Hide()
	a.chatRangeBtn = a.newHintButton(chatRangeIcon, "", a.showChatRange)
	a.setChatRangeLook()
	return a.chatRangeBtn, a.chatNav
}

// chatRangeText es el rango puesto en palabras ("Del 01/11/2026 al 16/11/2026"; "" sin rango).
func chatRangeText(from, to *time.Time) string {
	day := func(d *time.Time) string { return dayStart(*d).Format(i18n.T("chat.date_layout")) }
	switch {
	case from != nil && to != nil:
		return i18n.T("chat.range_both", day(from), day(to))
	case from != nil:
		return i18n.T("chat.range_from_only", day(from))
	case to != nil:
		return i18n.T("chat.range_to_only", day(to))
	}
	return ""
}

// setChatRangeLook pone el ícono (resaltado con un rango) y la leyenda del botón del rango.
func (a *App) setChatRangeLook() {
	if a.chatRangeBtn == nil {
		return
	}
	var icon fyne.Resource = chatRangeIcon
	hint := i18n.T("chat.range_hint")
	if text := chatRangeText(a.chatRangeFrom, a.chatRangeTo); text != "" {
		icon, hint = chatRangeOnIcon, text
	}
	a.chatRangeBtn.hint = hint
	if a.chatRangeBtn.Icon != icon {
		a.chatRangeBtn.SetIcon(icon)
	}
}

// setChatRange cambia el rango de fechas (nil = abierto) y vuelve a buscar.
func (a *App) setChatRange(from, to *time.Time) {
	a.chatRangeFrom, a.chatRangeTo = from, to
	a.setChatRangeLook()
	a.chatSearchChanged()
}

// clearChatSearchState saca el rango y la coincidencia actual (sin rearmar el chat): al cambiar de
// sala y al colgar.
func (a *App) clearChatSearchState() {
	a.chatRangeFrom, a.chatRangeTo, a.chatCurrentID = nil, nil, ""
	a.emptyChatRangeEntries()
	a.setChatRangeLook()
}

// emptyChatRangeEntries vacía "Desde" y "Hasta" sin que avisen (SetDate pasa por su OnChanged).
func (a *App) emptyChatRangeEntries() {
	if a.chatFromEntry == nil {
		return
	}
	onFrom, onTo := a.chatFromEntry.OnChanged, a.chatToEntry.OnChanged
	a.chatFromEntry.OnChanged, a.chatToEntry.OnChanged = nil, nil
	a.chatFromEntry.SetDate(nil)
	a.chatToEntry.SetDate(nil)
	a.chatFromEntry.OnChanged, a.chatToEntry.OnChanged = onFrom, onTo
}

// showChatRange abre, debajo del botón 📅, "Desde" y "Hasta" (días enteros; cualquiera puede quedar
// vacío) y "Limpiar".
func (a *App) showChatRange() {
	driver := fyne.CurrentApp().Driver()
	canvas := driver.CanvasForObject(a.chatRangeBtn)
	if canvas == nil {
		return
	}
	if a.chatRangePopup == nil {
		a.chatFromEntry = widget.NewDateEntry()
		a.chatToEntry = widget.NewDateEntry()
		a.chatFromEntry.OnChanged = func(d *time.Time) { a.setChatRange(d, a.chatRangeTo) }
		a.chatToEntry.OnChanged = func(d *time.Time) { a.setChatRange(a.chatRangeFrom, d) }
		clearBtn := widget.NewButtonWithIcon(i18n.T("chat.range_clear"), theme.ContentClearIcon(), func() {
			a.emptyChatRangeEntries()
			a.setChatRange(nil, nil)
		})
		closeBtn := widget.NewButtonWithIcon("", theme.CancelIcon(), func() { a.chatRangePopup.Hide() })
		closeBtn.Importance = widget.LowImportance
		form := container.New(layout.NewFormLayout(),
			widget.NewLabel(i18n.T("chat.range_from")), a.chatFromEntry,
			widget.NewLabel(i18n.T("chat.range_to")), a.chatToEntry)
		content := container.NewVBox(
			container.NewBorder(nil, nil, nil, closeBtn, cardTitle(i18n.T("chat.range_title"))),
			form,
			container.NewHBox(layout.NewSpacer(), clearBtn))
		a.chatRangePopup = widget.NewPopUp(content, canvas)
	}
	at := driver.AbsolutePositionForObject(a.chatRangeBtn)
	size := fyne.NewSize(max(a.chatRangePopup.MinSize().Width, 280), a.chatRangePopup.MinSize().Height)
	canvasSize := canvas.Size()
	x := min(at.X+a.chatRangeBtn.Size().Width-size.Width, canvasSize.Width-size.Width)
	a.chatRangePopup.ShowAtPosition(fyne.NewPos(max(0, x), at.Y+a.chatRangeBtn.Size().Height))
	a.chatRangePopup.Resize(size)
}
