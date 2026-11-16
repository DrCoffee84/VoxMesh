//go:build cgo

package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"voxmesh/internal/i18n"
	"voxmesh/internal/presence"
)

// lanRowsVisible son los renglones de "En tu red" que la tarjeta pide a la vista; con más, la lista
// hace scroll. Si la ventana es baja, muestra menos (hasta uno): ver lanColumnLayout.
const lanRowsVisible = 3

// newLANCard arma la tarjeta "En tu red" (F9) de la columna izquierda, debajo de Host y Cliente: un
// renglón por persona con VoxMesh abierto en la LAN o VPN. Hilo de UI.
func (a *App) newLANCard() *fyne.Container {
	a.lanHint = widget.NewLabel("")
	a.lanHint.Wrapping = fyne.TextWrapWord
	a.lanHint.Importance = widget.LowImportance
	a.lanList = container.NewVBox()
	a.lanScroll = container.NewVScroll(a.lanList)
	// La lista en el centro del Border: se estira con el alto que le dé lanColumnLayout.
	a.lanPanel = uiCard(container.NewBorder(container.NewVBox(cardTitle(i18n.T("lan.title")), a.lanHint), nil, nil, nil, a.lanScroll))
	a.showLANPeers(a.lanSnapshot())
	return a.lanPanel
}

// lanColumnLayout reparte lo que queda de la columna izquierda debajo de Host y Cliente entre "En tu
// red" (arriba, objects[0]) y el registro de actividad (objects[1]): la tarjeta pide hasta
// lanRowsVisible renglones (lanPreferredHeight) y el registro se queda con el resto. Si no alcanza,
// la tarjeta baja hasta su mínimo (un renglón, con scroll) antes que achicar el registro: así la
// ventana de fábrica (620 px de alto) entra con la tarjeta. Oculta (dentro de la sala), todo para
// el registro.
type lanColumnLayout struct{ app *App }

func (l lanColumnLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	card, log := objects[0], objects[1]
	size := log.MinSize()
	if card.Visible() {
		cardMin := card.MinSize()
		size = fyne.NewSize(max(size.Width, cardMin.Width), size.Height+cardMin.Height+theme.Padding())
	}
	return size
}

func (l lanColumnLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	card, log := objects[0], objects[1]
	top := float32(0)
	if card.Visible() {
		cardMin := card.MinSize().Height
		room := size.Height - log.MinSize().Height - theme.Padding()
		height := max(cardMin, min(l.app.lanPreferredHeight(), room))
		card.Move(fyne.NewPos(0, 0))
		card.Resize(fyne.NewSize(size.Width, height))
		top = height + theme.Padding()
	}
	log.Move(fyne.NewPos(0, top))
	log.Resize(fyne.NewSize(size.Width, max(0, size.Height-top)))
}

// lanPreferredHeight es el alto de la tarjeta con hasta lanRowsVisible renglones a la vista: su
// mínimo (un renglón) más los que faltan. Hilo de UI.
func (a *App) lanPreferredHeight() float32 {
	height := a.lanPanel.MinSize().Height
	rows := a.lanList.Objects
	if !a.lanScroll.Visible() || len(rows) < 2 {
		return height
	}
	extra := min(len(rows), lanRowsVisible) - 1
	return height + float32(extra)*(rows[0].MinSize().Height+theme.Padding())
}

// refreshLANCard vuelve a dibujar la tarjeta con el estado de ahora. Desde cualquier goroutine (el
// servicio la llama cuando cambia la lista): lee el estado dentro del fyne.Do, así vale el último.
func (a *App) refreshLANCard() {
	fyne.Do(func() { a.showLANPeers(a.lanSnapshot()) })
}

// showLANPeers dibuja la tarjeta (hilo de UI). Sin nadie, apagado o con el puerto ocupado, un texto
// gris dice por qué.
func (a *App) showLANPeers(state lanState, peers []presence.Peer) {
	if a.lanList == nil {
		return
	}
	hint := ""
	switch {
	case !a.conf().LANPresence:
		hint = i18n.T("lan.off")
	case state == lanFailed:
		hint = i18n.T("lan.unavailable", presence.Port)
	case len(peers) == 0:
		hint = i18n.T("lan.empty")
	}
	if hint != "" {
		peers = nil
		a.lanHint.SetText(hint)
		a.lanHint.Show()
	} else {
		a.lanHint.Hide()
	}
	rows := make([]fyne.CanvasObject, 0, len(peers))
	for _, peer := range peers {
		rows = append(rows, lanPeerRow(peer))
	}
	a.lanList.Objects = rows
	a.lanList.Refresh()
	if len(rows) == 0 {
		a.lanScroll.Hide()
	} else {
		a.lanScroll.SetMinSize(fyne.NewSize(0, rows[0].MinSize().Height)) // el mínimo: un renglón
		a.lanScroll.Show()
	}
	// Otra cantidad de renglones cambia cuánto pide la tarjeta: que la columna la vuelva a repartir.
	if a.lanColumn != nil {
		a.lanColumn.Refresh()
	}
}

// lanPeerRow es el renglón de una persona: el punto verde (disponible) o naranja (en una sala), el
// nombre (cortado con "…" si no entra: nunca ensancha la columna) y el estado a la derecha.
func lanPeerRow(peer presence.Peer) fyne.CanvasObject {
	dotColor, status, importance := theme.ColorNameSuccess, i18n.T("lan.available"), widget.SuccessImportance
	if peer.InRoom {
		dotColor, status, importance = theme.ColorNameWarning, i18n.T("lan.in_room"), widget.WarningImportance
	}
	dot := container.NewCenter(container.NewGridWrap(fyne.NewSize(10, 10), newThemedCircle(dotColor)))
	name := widget.NewLabel(peer.Name)
	name.Truncation = fyne.TextTruncateEllipsis
	state := widget.NewLabel(status)
	state.Importance = importance
	return container.NewBorder(nil, nil, dot, state, name)
}
