//go:build cgo

package ui

import (
	"image/color"
	"net/netip"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/atotto/clipboard"

	"voxmesh/internal/i18n"
	"voxmesh/internal/invite"
	"voxmesh/internal/logging"
	"voxmesh/internal/netinfo"
	"voxmesh/internal/transport"
)

// showInviteMenu (hilo de UI, botón "Invitar"): copia la invitación a la sala. Cualquiera de la
// sala puede invitar: el cliente copia la dirección del host tal como la ve él; el host ve el
// estado de su red (inviteOptions) y, si lo activó en Ajustes, las direcciones LAN/VPN.
func (a *App) showInviteMenu(button *widget.Button) {
	udp := a.transport.Load()
	endpoint := a.endpointText()
	if udp == nil || endpoint == "" {
		a.setStatus(i18n.M("invite.no_room"))
		return
	}
	key := udp.Key()
	if !a.hostMode.Load() {
		a.copyInvite(endpoint, key, 0)
		return
	}
	var network hostNetwork // nil (recién quedó de host): buscando
	if current := a.hostNet.Load(); current != nil {
		network = *current
	}
	port := udp.Address().Port
	showLAN := a.conf().ShowLANInvites
	go func() {
		var interfaces []netinfo.InterfaceIP
		if showLAN {
			var err error
			if interfaces, err = netinfo.LocalIPv4s(); err != nil {
				logging.Errorf("listar interfaces para la invitación: %s", logging.FormatError(err))
			}
		}
		options := inviteOptions(network, interfaces, port, showLAN)
		fyne.Do(func() {
			if copiesDirectly(options) {
				a.copyInvite(options[0].address, key, 0)
				return
			}
			a.showInvitePopup(button, options, key)
		})
	}()
}

// showInvitePopup (hilo de UI) muestra las opciones debajo del botón. Es un popup propio y no un
// fyne.Menu: el menú de Fyne pinta todos los renglones con el color del tema (y en gris los
// deshabilitados, ícono incluido), y acá el estado de la red va en naranja o rojo.
func (a *App) showInvitePopup(button *widget.Button, options []inviteOption, key transport.Key) {
	target := fyne.CurrentApp().Driver().CanvasForObject(button)
	if target == nil {
		return
	}
	var pop *widget.PopUp
	rows := make([]fyne.CanvasObject, 0, len(options))
	for _, option := range options {
		var onTap func()
		if option.address != "" {
			onTap = func() {
				pop.Hide()
				a.copyInvite(option.address, key, option.closedPort)
			}
		}
		rows = append(rows, newInviteRow(option.label, inviteToneColor(option.tone), onTap))
	}
	pop = widget.NewPopUp(container.NewVBox(rows...), target)
	pop.ShowAtRelativePosition(fyne.NewPos(0, button.Size().Height), button)
}

func inviteToneColor(tone inviteTone) color.Color {
	switch tone {
	case toneWarning:
		return theme.Color(theme.ColorNameWarning)
	case toneError:
		return theme.Color(theme.ColorNameError)
	case toneMuted:
		return theme.Color(theme.ColorNameDisabled)
	}
	return theme.Color(theme.ColorNameForeground)
}

// inviteRow es un renglón del popup de "Invitar": texto de color y, si se puede elegir, resaltado
// al pasar el mouse. El resaltado cambia de color en vez de mostrarse y ocultarse: un objeto
// oculto desde que se creó no se repinta con Refresh (ver tooltip). Solo el hilo de UI.
type inviteRow struct {
	widget.BaseWidget
	text  *canvas.Text
	hover *canvas.Rectangle
	onTap func() // nil = solo informa
}

func newInviteRow(label string, textColor color.Color, onTap func()) *inviteRow {
	row := &inviteRow{
		text:  canvas.NewText(label, textColor),
		hover: canvas.NewRectangle(color.Transparent),
		onTap: onTap,
	}
	row.hover.CornerRadius = theme.InputRadiusSize()
	row.ExtendBaseWidget(row)
	return row
}

func (r *inviteRow) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewStack(r.hover, container.NewPadded(r.text)))
}

func (r *inviteRow) Tapped(*fyne.PointEvent) {
	if r.onTap != nil {
		r.onTap()
	}
}

func (r *inviteRow) MouseIn(*desktop.MouseEvent)    { r.setHover(true) }
func (r *inviteRow) MouseMoved(*desktop.MouseEvent) {}
func (r *inviteRow) MouseOut()                      { r.setHover(false) }

func (r *inviteRow) Cursor() desktop.Cursor {
	if r.onTap != nil {
		return desktop.PointerCursor
	}
	return desktop.DefaultCursor
}

func (r *inviteRow) setHover(on bool) {
	if r.onTap == nil {
		return
	}
	r.hover.FillColor = color.Transparent
	if on {
		r.hover.FillColor = theme.Color(theme.ColorNameHover)
	}
	r.hover.Refresh()
}

// inviteFindingIcon: el botón "Invitar" mientras se obtiene la IP pública (un reloj naranja).
var inviteFindingIcon = theme.NewWarningThemedResource(theme.HistoryIcon())

// setHostNet cambia el estado de la red del host (nil = no soy host) y el ícono de "Invitar".
func (a *App) setHostNet(network *hostNetwork) {
	a.hostNet.Store(network)
	a.refreshInviteButton()
}

// refreshInviteButton: mientras se obtiene la IP pública, el botón "Invitar" muestra un reloj
// naranja, para verlo sin abrir el menú; después vuelve el ícono de copiar. Lee el estado dentro
// del fyne.Do: vale el último aunque se encolen dos.
func (a *App) refreshInviteButton() {
	fyne.Do(func() {
		if a.inviteButton == nil {
			return
		}
		icon := theme.ContentCopyIcon()
		if network := a.hostNet.Load(); network != nil && network.state == hostNetFinding {
			icon = inviteFindingIcon
		}
		a.inviteButton.SetIcon(icon)
	})
}

// copyInvite (hilo de UI) copia al portapapeles la invitación con address y la clave de la sala.
// La invitación nunca va al log ni a la barra de estado: lleva la clave. closedPort != 0: el router
// no abrió ese puerto y el aviso lo dice.
func (a *App) copyInvite(address string, key transport.Key, closedPort uint16) {
	addr, err := netip.ParseAddrPort(address)
	if err != nil {
		a.setStatus(i18n.M("invite.address_not_ready"))
		return
	}
	if err := clipboard.WriteAll(invite.Format(addr, key)); err != nil {
		a.setStatus(i18n.M("invite.copy_failed", err.Error()))
		return
	}
	if closedPort != 0 {
		a.setStatus(i18n.M("invite.copied_port_closed", address, closedPort))
		return
	}
	// Con el aviso: la invitación es la llave de la sala.
	a.setStatus(i18n.M("invite.copied", address))
}

// setRejoinInvite guarda en memoria la invitación para volver a entrar (con la dirección de quien
// va a quedar de host) y la deja en la entrada de "Conectar". Nunca va a disco.
func (a *App) setRejoinInvite(address string, key transport.Key) {
	addr, err := netip.ParseAddrPort(address)
	if err != nil {
		return
	}
	code := invite.Format(addr, key)
	a.rejoinInvite.Store(&code)
	fyne.Do(func() {
		if a.endpointEntry != nil {
			a.endpointEntry.SetText(code)
		}
	})
}
