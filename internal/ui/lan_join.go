//go:build cgo

package ui

import (
	"net/netip"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"voxmesh/internal/audio"
	"voxmesh/internal/i18n"
	"voxmesh/internal/identity"
	"voxmesh/internal/logging"
	"voxmesh/internal/presence"
	"voxmesh/internal/room"
)

// Entrar a la sala de alguien de "En tu red" (AGENDA F10, AGENT.md §3.N). El que pide toca Entrar
// en el renglón naranja; el servicio hace el desafío y, con la firma buena, el de la sala decide
// acá (onJoinEvent):
//   - la llave está en la lista de confianza de la sala (a.trust, §3.E6): le manda la invitación,
//     sin que nadie toque nada, y queda una línea en el registro de actividad;
//   - si no, un pedido pendiente (knock): suena un "toc toc" (audio.SFXKnock) y queda en el
//     registro de actividad, en el menú del ícono de la bandeja y en un cartel dentro de la
//     ventana, sin showMainWindow ni robar el foco: nunca saca el juego. Vence a
//     presence.JoinTimeout.

// knock es un pedido pendiente para entrar a mi sala. Solo el hilo de UI.
type knock struct {
	join presence.ID
	peer netip.AddrPort
	name string
	key  identity.Key
}

// joinAdmittedClose: con la invitación, el cartel del que pide dice "Entrando…" este tiempo.
const joinAdmittedClose = 1500 * time.Millisecond

// knockCue hace sonar el "toc toc" (los tests lo cambian para no abrir el audio).
var knockCue = func(a *App) { a.playCue(audio.SFXKnock) }

// joinPeer es el botón Entrar (hilo de UI): pide entrar a la sala de peer y abre el cartel
// "Esperando que Budo te deje entrar… [Cancelar]".
func (a *App) joinPeer(peer presence.Peer) {
	service := a.lanService()
	if service == nil || a.callUI != nil {
		return
	}
	if a.connected() {
		a.setStatus(i18n.M("conn.udp_exists"))
		return
	}
	a.callSeq.Add(1)
	cancel := widget.NewButtonWithIcon(i18n.T("common.cancel"), theme.CancelIcon(), a.cancelJoin)
	ui := a.openCallUI(i18n.T("lan.join_title"), peer.Name, i18n.T("lan.join_waiting", peer.Name), cancel)
	ui.seq, ui.joining = a.callSeq.Load(), true
	seq := ui.seq
	name := a.lanStatus().Name
	go func() {
		if _, err := service.Join(peer.Addr, name); err != nil {
			logging.Infof("Red local: no se pudo pedir entrar a %s: %s", peer.Addr, err)
			fyne.Do(func() { a.finishOutgoingCall(seq, i18n.T("lan.join_failed", err.Error())) })
			return
		}
		logging.Infof("Red local: pidiendo entrar a la sala de %q (%s)", peer.Name, peer.Addr)
	}()
}

// cancelJoin es Cancelar (hilo de UI).
func (a *App) cancelJoin() {
	a.callSeq.Add(1)
	if service := a.lanService(); service != nil {
		service.CancelJoin()
	}
	a.closeCallUI()
}

// onJoinEvent llega desde el servicio.
func (a *App) onJoinEvent(event presence.JoinEvent) {
	switch event.Kind {
	case presence.JoinRequest:
		a.handleJoinRequest(event)
	case presence.JoinCanceled, presence.JoinExpired:
		fyne.Do(func() { a.removeKnock(event) })
	case presence.JoinAdmitted, presence.JoinDeclined:
		fyne.Do(func() { a.showJoinResult(event) })
	}
}

// handleJoinRequest (goroutine del servicio): alguien que probó su llave pide entrar. De confianza,
// entra solo; si no, queda pendiente.
func (a *App) handleJoinRequest(event presence.JoinEvent) {
	service := a.lanService()
	if service == nil {
		return
	}
	if !a.connected() {
		service.RejectJoin(event.Join, presence.ReasonNoRoom)
		return
	}
	if !a.trust.has(event.Key) {
		logging.Infof("Red local: %q (%s, %s) pide entrar: pendiente", event.Name, event.Peer, event.Key.Short())
		fyne.Do(func() { a.addKnock(knock{join: event.Join, peer: event.Peer, name: event.Name, key: event.Key}) })
		return
	}
	if !a.admitJoin(service, event.Join, event.Peer) {
		return
	}
	msg := i18n.M("lan.join_trusted", event.Name)
	logging.Infof("Registro de actividad: %s (%s)", msg.In(i18n.ES), event.Key.Short())
	a.addActivity(msg.String())
}

// admitJoin le manda a peer la invitación a mi sala (como en la llamada: la IP de la interfaz que
// llega a él si soy el host, la del host si soy cliente). Nunca al log: lleva la clave.
func (a *App) admitJoin(service *presence.Service, join presence.ID, peer netip.AddrPort) bool {
	code, err := a.callInvite(peer)
	if err != nil {
		logging.Infof("Red local: no salió la invitación para %s: %s", peer, err)
		service.RejectJoin(join, presence.ReasonNoRoom)
		return false
	}
	if err := service.AdmitJoin(join, code); err != nil {
		logging.Infof("Red local: el pedido de %s ya no estaba: %s", peer, err)
		return false
	}
	return true
}

// addKnock suma un pedido pendiente (hilo de UI): suena, queda en el registro, en la bandeja y en
// el cartel. Sin showMainWindow: la ventana se queda como está.
func (a *App) addKnock(k knock) {
	for _, existing := range a.knocks {
		if existing.join == k.join {
			return
		}
	}
	a.knocks = append(a.knocks, k)
	go knockCue(a) // siempre, como el timbre: con la app en la bandeja es el aviso
	msg := i18n.M("lan.knock_log", k.name)
	go func() {
		logging.Infof("Registro de actividad: %s", msg.In(i18n.ES))
		a.addActivity(msg.String())
	}()
	a.refreshKnocks()
}

// removeKnock saca un pedido que se terminó sin respuesta (canceló, venció o salí de la sala).
func (a *App) removeKnock(event presence.JoinEvent) {
	if !a.dropKnock(event.Join) {
		return
	}
	var key string
	switch {
	case event.Kind == presence.JoinCanceled:
		key = "lan.knock_canceled"
	case event.Reason == presence.ReasonNoAnswer:
		key = "lan.knock_expired"
	default:
		return // salí de la sala: no hace falta decir nada
	}
	a.knockActivity(i18n.M(key, event.Name))
}

// dropKnock saca el pedido join de la lista (y de la bandeja y el cartel). false si no estaba.
func (a *App) dropKnock(join presence.ID) bool {
	for i, k := range a.knocks {
		if k.join == join {
			a.knocks = append(a.knocks[:i:i], a.knocks[i+1:]...)
			a.refreshKnocks()
			return true
		}
	}
	return false
}

// knockActivity anota msg en el registro de actividad (y en el log en castellano).
func (a *App) knockActivity(msg i18n.Msg) {
	go func() {
		logging.Infof("Registro de actividad: %s", msg.In(i18n.ES))
		a.addActivity(msg.String())
	}()
}

// answerKnock es Aceptar / Rechazar de un pedido pendiente, desde el cartel o la bandeja (hilo de
// UI). always ("Dejar entrar siempre", solo en el cartel) suma su llave a la lista de la sala.
func (a *App) answerKnock(join presence.ID, accept, always bool) {
	var k knock
	found := false
	for _, existing := range a.knocks {
		if existing.join == join {
			k, found = existing, true
			break
		}
	}
	if !found || !a.dropKnock(join) {
		return
	}
	service := a.lanService()
	me := a.lanStatus().Name
	go func() {
		if service == nil {
			return
		}
		if !accept {
			service.RejectJoin(k.join, presence.ReasonRejected)
			a.knockActivity(i18n.M("lan.knock_rejected", k.name))
			return
		}
		if !a.admitJoin(service, k.join, k.peer) {
			a.knockActivity(i18n.M("lan.knock_gone", k.name))
			return
		}
		if !always {
			a.knockActivity(i18n.M("lan.knock_accepted", k.name))
			return
		}
		a.addTrusted(room.Trusted{Key: k.key.String(), Name: k.name, By: me, At: time.Now().UTC()})
		logging.Infof("Lista de confianza: suma a %q (%s), marcado por %q", k.name, k.key.Short(), me)
		a.knockActivity(i18n.M("lan.knock_accepted_always", k.name))
	}()
}

// refreshKnocks vuelve a armar el cartel de los pedidos y el menú de la bandeja (hilo de UI). El
// cartel muestra el primero y, si hay más, cuántos: así varios a la vez (hasta
// presence.MaxJoins) no agrandan la ventana. Los demás están en la bandeja y aparecen al contestar.
func (a *App) refreshKnocks() {
	if a.knockBox != nil {
		var rows []fyne.CanvasObject
		if len(a.knocks) > 0 {
			rows = append(rows, a.knockRow(a.knocks[0]))
		}
		if more := len(a.knocks) - 1; more > 0 {
			label := widget.NewLabel(i18n.T("lan.knock_more", more))
			label.Importance = widget.LowImportance
			label.Truncation = fyne.TextTruncateEllipsis
			rows = append(rows, label)
		}
		a.knockBox.Objects = rows
		a.knockBox.Refresh()
	}
	a.refreshTrayMenu()
}

// knockRow es el cartel de un pedido, arriba del chat, en tres líneas cortas (así entra en la
// columna del centro también con ☰ abierto): "Juan quiere entrar" (cortado con "…" si no entra),
// "☐ Dejar entrar siempre" y [Rechazar] [Aceptar].
func (a *App) knockRow(k knock) fyne.CanvasObject {
	text := widget.NewLabelWithStyle(i18n.T("lan.knock", k.name), fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	text.Truncation = fyne.TextTruncateEllipsis
	always := widget.NewCheck(i18n.T("lan.knock_always"), nil)
	reject := widget.NewButtonWithIcon(i18n.T("lan.reject"), theme.CancelIcon(), func() { a.answerKnock(k.join, false, false) })
	reject.Importance = widget.DangerImportance
	accept := widget.NewButtonWithIcon(i18n.T("lan.accept"), theme.ConfirmIcon(), func() { a.answerKnock(k.join, true, always.Checked) })
	accept.Importance = widget.SuccessImportance
	title := container.NewBorder(nil, nil, widget.NewIcon(theme.AccountIcon()), nil, text)
	return uiCard(container.NewVBox(title, always, container.NewGridWithColumns(2, reject, accept)))
}

// showJoinResult (hilo de UI, al que pide): llegó la invitación o no lo dejaron entrar.
func (a *App) showJoinResult(event presence.JoinEvent) {
	ui := a.callUI
	if ui == nil || !ui.joining {
		return
	}
	if event.Kind == presence.JoinAdmitted {
		seq := ui.seq
		a.callSeq.Add(1)
		a.setCallText(i18n.T("lan.join_entering"))
		ui.buttons.Objects = nil
		ui.buttons.Refresh()
		logging.Infof("Red local: %s me dejó entrar", event.Peer)
		a.connectClientVia(event.Invite, true) // como si la hubiera pegado (la valida invite.Parse); no da confianza (F11)
		time.AfterFunc(joinAdmittedClose, func() {
			fyne.Do(func() {
				if a.callUI == ui && ui.seq == seq {
					a.closeCallUI()
				}
			})
		})
		return
	}
	key := "lan.join_rejected"
	switch event.Reason {
	case presence.ReasonBusy:
		key = "lan.join_busy"
	case presence.ReasonNoRoom:
		key = "lan.join_no_room"
	case presence.ReasonNoAnswer:
		key = "lan.join_no_answer"
	}
	msg := i18n.M(key, ui.name)
	if key == "lan.join_no_answer" {
		msg = i18n.M(key) // "Nadie contestó.": sin nombre
	}
	logging.Infof("Red local: %s", msg.In(i18n.ES))
	a.finishOutgoingCall(ui.seq, msg.String())
}
