//go:build cgo

package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"voxmesh/internal/i18n"
	"voxmesh/internal/logging"
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
// mínimo (el primer renglón) más los que faltan, cada uno con su alto (el de alguien en una sala
// ocupa dos líneas, F10). Hilo de UI.
func (a *App) lanPreferredHeight() float32 {
	height := a.lanPanel.MinSize().Height
	rows := a.lanList.Objects
	if !a.lanScroll.Visible() {
		return height
	}
	for _, row := range rows[1:min(len(rows), lanRowsVisible)] {
		height += row.MinSize().Height + theme.Padding()
	}
	return height
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
	free := !a.connected() // Entrar solo si no estoy en una sala (F10)
	rows := make([]fyne.CanvasObject, 0, len(peers))
	for _, peer := range peers {
		var onJoin func()
		if free {
			onJoin = func() { a.joinPeer(peer) }
		}
		rows = append(rows, lanPeerRow(peer, func() { a.callPeer(peer) }, onJoin))
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

// callIcon es el teléfono de Material ("call"), del color del texto.
var callIcon = theme.NewThemedResource(fyne.NewStaticResource("call.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24"><path d="M20.01 15.38c-1.23 0-2.42-.2-3.53-.56-.35-.12-.74-.03-1.01.24l-1.57 1.97c-2.83-1.35-5.48-3.9-6.89-6.83l1.95-1.66c.27-.28.35-.67.24-1.02-.37-1.11-.56-2.3-.56-3.53 0-.54-.45-.99-.99-.99H4.19C3.65 3 3 3.24 3 3.99 3 13.28 10.73 21 20.01 21c.71 0 .99-.63.99-1.18v-3.45c0-.54-.45-.99-.99-.99z"/></svg>`)))

// lanPeerRow es el renglón de una persona: el punto verde (disponible) o naranja (en una sala), el
// nombre (cortado con "…" si no entra: nunca ensancha la columna) y a la derecha Llamar (onCall).
// Si está en una sala (F10) va en dos líneas, porque entero no entra con el botón: el nombre arriba
// y "en «Mi sala»" abajo, más chico y en naranja (los dos cortados con "…"), con Entrar (onJoin;
// nil = sin botón: estoy en una sala). Por ahora no se llama a alguien que está en otra llamada.
func lanPeerRow(peer presence.Peer, onCall, onJoin func()) fyne.CanvasObject {
	name := widget.NewLabel(peer.Name)
	name.Truncation = fyne.TextTruncateEllipsis
	if !peer.InRoom {
		call := widget.NewButtonWithIcon(i18n.T("lan.call"), callIcon, onCall)
		call.Importance = widget.LowImportance
		return container.NewBorder(nil, nil, lanDot(theme.ColorNameSuccess), call, name)
	}
	roomText := i18n.T("lan.in_room")
	if peer.Room != "" {
		roomText = i18n.T("lan.in_room_named", peer.Room)
	}
	roomLabel := widget.NewLabel(roomText)
	roomLabel.Truncation = fyne.TextTruncateEllipsis
	roomLabel.Importance = widget.WarningImportance
	roomLabel.SizeName = theme.SizeNameCaptionText
	// Los dos labels se pisan el relleno: juntos, como dos líneas de un mismo renglón.
	lines := container.New(layout.NewCustomPaddedVBoxLayout(-2*theme.InnerPadding()), name, roomLabel)
	var right fyne.CanvasObject
	if onJoin != nil {
		join := widget.NewButtonWithIcon(i18n.T("lan.join"), theme.LoginIcon(), onJoin)
		join.Importance = widget.LowImportance
		right = join
	}
	return container.NewBorder(nil, nil, lanDot(theme.ColorNameWarning), right, lines)
}

// lanDot es el punto de color del renglón.
func lanDot(color fyne.ThemeColorName) fyne.CanvasObject {
	return container.NewCenter(container.NewGridWrap(fyne.NewSize(10, 10), newThemedCircle(color)))
}

// callUI es el cartel de una llamada: "Llamando a Juan… [Cortar]" para el que llama, "Coffee te
// está llamando · Sala: Los pibes · [Rechazar] [Atender]" para el llamado. Solo el hilo de UI.
type callUI struct {
	dialog  dialog.Dialog
	text    *widget.Label
	buttons *fyne.Container
	name    string // el otro
	seq     uint64 // del que llama o pide entrar: a.callSeq de esta llamada
	joining bool   // es un pedido para entrar a la sala del otro (F10), no una llamada
}

// openCallUI abre el cartel title con text y buttons.
func (a *App) openCallUI(title, name, text string, buttons ...fyne.CanvasObject) *callUI {
	ui := &callUI{text: widget.NewLabel(text), buttons: container.NewGridWithColumns(max(1, len(buttons)), buttons...), name: name}
	ui.text.Wrapping = fyne.TextWrapWord
	ui.text.Alignment = fyne.TextAlignCenter
	ui.dialog = dialog.NewCustomWithoutButtons(title, container.NewVBox(ui.text, ui.buttons), a.window)
	ui.dialog.Resize(fyne.NewSize(360, 0))
	ui.dialog.Show()
	a.callUI = ui
	return ui
}

// showOutgoingCall: "Llamando a Juan…" con Cortar.
func (a *App) showOutgoingCall(name string) {
	ui := a.openCallUI(i18n.T("lan.call_title"), name, i18n.T("lan.calling", name), widget.NewButtonWithIcon(i18n.T("lan.hang_up"), theme.CancelIcon(), a.hangUpCall))
	ui.seq = a.callSeq.Load()
}

// finishOutgoingCall deja el cartel del que llama con el resultado y Cerrar (si sigue siendo esa
// llamada).
func (a *App) finishOutgoingCall(seq uint64, text string) {
	if a.callUI == nil || a.callUI.seq != seq {
		return
	}
	a.callSeq.Add(1)
	a.setCallText(text)
	a.callUI.buttons.Objects = []fyne.CanvasObject{widget.NewButton(i18n.T("common.close"), a.closeCallUI)}
	a.callUI.buttons.Refresh()
}

// setCallText cambia el texto del cartel.
func (a *App) setCallText(text string) {
	if a.callUI != nil {
		a.callUI.text.SetText(text)
	}
}

// closeCallUI cierra el cartel (y el timbre).
func (a *App) closeCallUI() {
	a.stopRing()
	if a.callUI != nil {
		a.callUI.dialog.Hide()
		a.callUI = nil
	}
}

// showCallEvent muestra lo que pasó con la llamada (hilo de UI, onCallEvent).
func (a *App) showCallEvent(event presence.CallEvent) {
	switch event.Kind {
	case presence.CallIncoming:
		if a.callUI != nil {
			// Estoy llamando a otro (todavía abriendo la sala): esta no se atiende.
			if service := a.lanService(); service != nil {
				service.Answer(false)
			}
			return
		}
		a.showMainWindow() // aunque esté en la bandeja
		text := i18n.T("lan.incoming", event.Name)
		if event.Room != "" {
			text += "\n" + i18n.T("lan.incoming_room", event.Room)
		}
		reject := widget.NewButtonWithIcon(i18n.T("lan.reject"), theme.CancelIcon(), func() { a.answerCall(false) })
		reject.Importance = widget.DangerImportance
		answer := widget.NewButtonWithIcon(i18n.T("lan.answer"), callIcon, func() { a.answerCall(true) })
		answer.Importance = widget.SuccessImportance
		a.openCallUI(i18n.T("lan.call_title"), event.Name, text, reject, answer)
		a.ring()
		logging.Infof("Red local: llamada de %q (%s)", event.Name, event.Peer)
	case presence.CallIncomingEnded:
		if a.callUI != nil && a.callUI.seq == 0 {
			a.closeCallUI()
			a.setStatus(i18n.M("lan.call_ended", event.Name))
		}
	case presence.CallInvite:
		a.closeCallUI()
		a.connectClient(event.Invite) // como si la hubiera pegado (la valida invite.Parse)
	case presence.CallInviteMissing:
		if a.callUI != nil {
			a.setCallText(i18n.T("lan.invite_missing"))
			a.callUI.buttons.Objects = []fyne.CanvasObject{widget.NewButton(i18n.T("common.close"), a.closeCallUI)}
			a.callUI.buttons.Refresh()
		}
	case presence.CallAccepted:
		if a.callUI != nil && !a.callUI.joining {
			a.setStatus(i18n.M("lan.accepted", a.callUI.name))
			a.closeCallUI()
		}
	case presence.CallDeclined, presence.CallNoAnswer:
		if a.callUI == nil || a.callUI.joining {
			return
		}
		key := "lan.no_answer"
		switch {
		case event.Kind == presence.CallNoAnswer:
		case event.Reason == presence.ReasonBusy:
			key = "lan.busy"
		case event.Reason == presence.ReasonInRoom:
			key = "lan.busy_in_room"
		default:
			key = "lan.declined"
		}
		msg := i18n.M(key, a.callUI.name)
		logging.Infof("%s", msg.In(i18n.ES))
		a.finishOutgoingCall(a.callUI.seq, msg.String())
	}
}
