//go:build cgo

package ui

import (
	"net"
	"net/netip"
	"strconv"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
	"github.com/atotto/clipboard"

	"voxmesh/internal/invite"
	"voxmesh/internal/logging"
	"voxmesh/internal/netinfo"
	"voxmesh/internal/transport"
)

// inviteWarning acompaña cada invitación copiada: la invitación es la llave de la sala.
const inviteWarning = "Quien tenga la invitación puede entrar."

// inviteOption es una dirección para la invitación (la pública, o una de LAN/VPN).
type inviteOption struct {
	label   string
	address string
}

// showInviteMenu (hilo de UI, botón "Invitar"): copia la invitación a la sala. Cualquiera de la
// sala puede invitar: el cliente copia la dirección del host tal como la ve él; el host elige
// entre la pública y la de cada interfaz (LAN, Tailscale, ZeroTier, Radmin…).
func (a *App) showInviteMenu(button *widget.Button) {
	udp := a.transport.Load()
	endpoint := a.endpointText()
	if udp == nil || endpoint == "" {
		a.setStatus("Primero creá una sala o entrá a una.")
		return
	}
	key := udp.Key()
	if !a.hostMode.Load() {
		a.copyInvite(endpoint, key)
		return
	}
	port := udp.Address().Port
	go func() {
		options := hostInviteOptions(endpoint, port)
		fyne.Do(func() {
			if len(options) == 1 {
				a.copyInvite(options[0].address, key)
				return
			}
			items := make([]*fyne.MenuItem, 0, len(options))
			for _, option := range options {
				items = append(items, fyne.NewMenuItem(option.label, func() { a.copyInvite(option.address, key) }))
			}
			canvas := fyne.CurrentApp().Driver().CanvasForObject(button)
			if canvas == nil {
				return
			}
			widget.ShowPopUpMenuAtRelativePosition(fyne.NewMenu("", items...), canvas, fyne.NewPos(0, button.Size().Height), button)
		})
	}()
}

// hostInviteOptions: la dirección de la sala si es pública ("Internet") y la de cada interfaz con
// el puerto donde escucha el socket ("LAN/VPN"). Lee las interfaces: fuera del hilo de UI.
func hostInviteOptions(endpoint string, listenPort int) []inviteOption {
	var options []inviteOption
	if addr, err := netip.ParseAddrPort(endpoint); err == nil && isInternetAddr(addr.Addr()) {
		options = append(options, inviteOption{label: "Internet · " + endpoint, address: endpoint})
	}
	interfaces, err := netinfo.LocalIPv4s()
	if err != nil {
		logging.Errorf("listar interfaces para la invitación: %s", logging.FormatError(err))
	}
	for _, iface := range interfaces {
		address := net.JoinHostPort(iface.IP.String(), strconv.Itoa(listenPort))
		options = append(options, inviteOption{label: "LAN/VPN · " + iface.Name + " · " + address, address: address})
	}
	if len(options) == 0 {
		options = append(options, inviteOption{label: endpoint, address: endpoint})
	}
	return options
}

// cgnat es 100.64.0.0/10: la usan Tailscale y los proveedores con CGNAT, nunca es alcanzable
// desde Internet.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

func isInternetAddr(ip netip.Addr) bool {
	return ip.IsGlobalUnicast() && !ip.IsPrivate() && !cgnat.Contains(ip)
}

// copyInvite (hilo de UI) copia al portapapeles la invitación con address y la clave de la sala.
// La invitación nunca va al log ni a la barra de estado: lleva la clave.
func (a *App) copyInvite(address string, key transport.Key) {
	addr, err := netip.ParseAddrPort(address)
	if err != nil {
		a.setStatus("La dirección de la sala todavía no está lista.")
		return
	}
	if err := clipboard.WriteAll(invite.Format(addr, key)); err != nil {
		a.setStatus("No se pudo copiar: " + err.Error())
		return
	}
	a.setStatus("Invitación copiada (" + address + "). " + inviteWarning)
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
