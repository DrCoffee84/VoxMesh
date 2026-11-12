//go:build cgo

package ui

import (
	"net"
	"net/netip"
	"strconv"

	"voxmesh/internal/i18n"
	"voxmesh/internal/netinfo"
)

// Estado de la red del host: si ya se puede invitar por internet. Lo arma configureHostNetwork
// (UPnP + IP pública) y lo leen el menú "Invitar", su botón y el registro de actividad. "Internet"
// sale de acá y nunca de la dirección de la sala: Radmin usa 26.0.0.0/8, que parece pública, y al
// quedar de host por migración la dirección sigue siendo la del host viejo hasta que esto termina.

type hostNetState int

const (
	hostNetFinding hostNetState = iota // abriendo el puerto (UPnP) y pidiendo la IP pública
	hostNetReady                       // puerto abierto e IP pública: se puede invitar por internet
	hostNetNoUPnP                      // hay IP pública, pero el router no abrió el puerto
	hostNetNoIP                        // no se pudo obtener la IP pública
)

// hostNetwork es una foto inmutable (a.hostNet, nil = no soy host). Quien empieza a ser host guarda
// una nueva en hostNetFinding y configureHostNetwork la reemplaza con CompareAndSwap: si mientras
// tanto la sala se cerró o dejó de ser host, el resultado viejo no pisa nada.
type hostNetwork struct {
	state   hostNetState
	public  netip.AddrPort // IP pública : puerto externo (con hostNetReady y hostNetNoUPnP)
	localIP netip.Addr     // IP de esta PC en la LAN (con hostNetNoUPnP: la pide el router para abrir el puerto)
}

// hostNetMsg es el texto del estado para la barra de estado y el registro de actividad: el mismo
// en los dos. Cada falla dice qué hacer y menciona LAN/VPN recién al final.
func hostNetMsg(n hostNetwork) i18n.Msg {
	switch n.state {
	case hostNetReady:
		return i18n.M("conn.net_ready")
	case hostNetNoUPnP:
		local := "?"
		if n.localIP.IsValid() {
			local = n.localIP.String()
		}
		return i18n.M("conn.net_no_upnp", n.public.Port(), local)
	case hostNetNoIP:
		return i18n.M("conn.net_no_ip")
	}
	return i18n.M("conn.net_finding")
}

// inviteTone es el color de un renglón del menú "Invitar".
type inviteTone int

const (
	toneNormal  inviteTone = iota
	toneWarning            // naranja: esperando, o el puerto no se abrió
	toneError              // rojo: por internet no se puede invitar
	toneMuted              // gris: solo informa
)

// inviteOption es un renglón del menú "Invitar" del host.
type inviteOption struct {
	label      string
	address    string     // "IP:puerto" que se copia; "" = no se puede elegir
	tone       inviteTone // color del texto
	closedPort uint16     // != 0: el router no abrió este puerto (al copiar se avisa)
}

// inviteOptions arma el menú "Invitar" del host. Primero el renglón de internet según el estado;
// con showLAN (Ajustes, apagado por defecto) también una dirección por interfaz con el puerto
// donde escucha el socket. Sin showLAN nunca ofrece una IP local: mientras se busca la IP pública
// solo se espera, y si internet falla, un renglón gris dice dónde activar LAN/VPN.
func inviteOptions(n hostNetwork, interfaces []netinfo.InterfaceIP, listenPort int, showLAN bool) []inviteOption {
	var options []inviteOption
	failed := true
	switch n.state {
	case hostNetReady:
		failed = false
		address := n.public.String()
		options = append(options, inviteOption{label: i18n.T("invite.internet", address), address: address})
	case hostNetNoUPnP:
		address := n.public.String()
		options = append(options, inviteOption{
			label:      i18n.T("invite.internet_port_closed", address),
			address:    address,
			tone:       toneWarning,
			closedPort: n.public.Port(),
		})
	case hostNetNoIP:
		options = append(options, inviteOption{label: i18n.T("invite.no_public_ip"), tone: toneError})
	default:
		failed = false // es cuestión de esperar unos segundos: no se sugiere LAN/VPN
		options = append(options, inviteOption{label: i18n.T("invite.finding"), tone: toneWarning})
	}
	if !showLAN {
		if failed {
			options = append(options, inviteOption{label: i18n.T("invite.lan_hint"), tone: toneMuted})
		}
		return options
	}
	for _, iface := range interfaces {
		address := net.JoinHostPort(iface.IP.String(), strconv.Itoa(listenPort))
		options = append(options, inviteOption{label: i18n.T("invite.lan", iface.Name, address), address: address})
	}
	return options
}

// copiesDirectly: con un solo renglón listo para copiar, "Invitar" copia sin abrir el menú. Con
// cualquier aviso (naranja, rojo) se abre igual, para que se vea.
func copiesDirectly(options []inviteOption) bool {
	return len(options) == 1 && options[0].address != "" && options[0].tone == toneNormal
}
