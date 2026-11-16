//go:build cgo

package ui

import (
	"image/color"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"voxmesh/internal/i18n"
)

// Fecha y hora exactas al pasar el mouse por la hora de un mensaje (AGENDA C39).

// chatStampFormat es la hora que muestra el renglón.
const chatStampFormat = "15:04"

// weekdayKeys son los nombres de los días (time.Weekday: domingo = 0); los de time están en inglés.
var weekdayKeys = [7]string{
	"chat.weekday_sunday", "chat.weekday_monday", "chat.weekday_tuesday", "chat.weekday_wednesday",
	"chat.weekday_thursday", "chat.weekday_friday", "chat.weekday_saturday",
}

// stampHint es la leyenda de la hora de un mensaje, en hora local y en lang: "lunes 16/11/2026 ·
// 22:53:07" / "Monday 11/16/2026 · 22:53:07".
func stampHint(at time.Time, lang i18n.Lang) string {
	local := at.Local()
	return i18n.In(lang, "chat.stamp_hint",
		i18n.In(lang, weekdayKeys[local.Weekday()]),
		local.Format(i18n.In(lang, "chat.date_layout")),
		local.Format("15:04:05"))
}

// chatStamp es la zona de la hora de un renglón del chat: transparente y encima de la hora que
// dibuja el RichText del mensaje (stampLayout), así el renglón se ve igual que antes, también
// cuando el texto parte la línea. Al dejar el mouse encima muestra stampHint por a.tooltip, como
// hintButton; solo sobre la hora, no sobre todo el renglón. Solo el hilo de UI.
type chatStamp struct {
	widget.BaseWidget
	app      *App
	at       time.Time
	text     string // la hora como se ve ("22:53")
	hovered  bool
	hoverGen int // descarta el timer de un MouseIn viejo
}

func (a *App) newChatStamp(at time.Time) *chatStamp {
	s := &chatStamp{app: a, at: at, text: at.Local().Format(chatStampFormat)}
	s.ExtendBaseWidget(s)
	return s
}

func (s *chatStamp) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(canvas.NewRectangle(color.Transparent))
}

// MinSize es lo que ocupa la hora dibujada (con la letra del RichText).
func (s *chatStamp) MinSize() fyne.Size {
	return fyne.MeasureText(s.text, theme.TextSize(), fyne.TextStyle{})
}

func (s *chatStamp) hint() string { return stampHint(s.at, i18n.Current()) }

func (s *chatStamp) MouseIn(*desktop.MouseEvent) {
	s.hovered = true
	s.hoverGen++
	gen := s.hoverGen
	time.AfterFunc(bindTooltipDelay, func() {
		fyne.Do(func() {
			if s.hovered && s.hoverGen == gen && s.app.tooltip != nil {
				s.app.tooltip.showLeading(s, s.hint())
			}
		})
	})
}

func (s *chatStamp) MouseMoved(*desktop.MouseEvent) {}

func (s *chatStamp) MouseOut() {
	s.hovered = false
	s.app.hideTooltip()
}

// stampLayout: objects[0] (el RichText del mensaje) ocupa todo y objects[1] (chatStamp) va donde
// el RichText dibuja su primer texto, la hora: a InnerPadding de arriba y de la izquierda.
type stampLayout struct{}

func (stampLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	return objects[0].MinSize()
}

func (stampLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	objects[0].Move(fyne.NewPos(0, 0))
	objects[0].Resize(size)
	pad := theme.InnerPadding()
	objects[1].Move(fyne.NewPos(pad, pad))
	objects[1].Resize(objects[1].MinSize())
}
