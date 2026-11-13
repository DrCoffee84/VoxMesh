//go:build cgo

package ui

import (
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"

	"voxmesh/internal/i18n"
	"voxmesh/internal/netinfo"
)

var testInterfaces = []netinfo.InterfaceIP{
	{Name: "Ethernet 4", IP: net.ParseIP("192.168.1.37").To4()},
	{Name: "Radmin VPN", IP: net.ParseIP("26.77.45.242").To4()},
}

const testListenPort = 47830

func testNetwork(state hostNetState) hostNetwork {
	n := hostNetwork{state: state}
	if state != hostNetFinding && state != hostNetNoIP {
		n.public = netip.MustParseAddrPort("181.10.20.30:47831")
	}
	switch state {
	case hostNetNoUPnP:
		n.localIP = netip.MustParseAddr("192.168.1.37")
	case hostNetCGNAT:
		n.routerWAN = netip.MustParseAddr("100.70.1.2")
	case hostNetDoubleNAT:
		n.routerWAN = netip.MustParseAddr("192.168.0.10")
	}
	return n
}

// Cada estado de la red del host × el check de LAN/VPN: arriba, cómo está internet (si el puerto
// no se abrió, el error en rojo y abajo la dirección de internet en naranja); sin el check nunca
// aparece una IP local, y LAN/VPN se sugiere (en gris) solo cuando internet falló, nunca mientras
// se espera. Los renglones rojos no copian: muestran el registro de actividad.
func TestInviteOptions(t *testing.T) {
	type row struct {
		tone inviteTone
		copy bool // copia la IP pública
	}
	cases := []struct {
		state    hostNetState
		internet []row // los renglones de internet, en orden
		lanHint  bool
		direct   bool // "Invitar" copia sin menú
	}{
		{hostNetFinding, []row{{toneWarning, false}}, false, false},
		{hostNetReady, []row{{toneNormal, true}}, false, true},
		{hostNetNoUPnP, []row{{toneError, false}, {toneWarning, true}}, true, false},
		{hostNetNoIP, []row{{toneError, false}}, true, false},
		{hostNetCGNAT, []row{{toneError, false}}, true, false},
		{hostNetDoubleNAT, []row{{toneWarning, true}}, true, false},
	}
	for _, c := range cases {
		for _, showLAN := range []bool{false, true} {
			network := testNetwork(c.state)
			options := inviteOptions(network, testInterfaces, testListenPort, showLAN)
			if len(options) < len(c.internet) {
				t.Fatalf("estado %d, LAN %v: %+v", c.state, showLAN, options)
			}
			for i, want := range c.internet {
				got := options[i]
				if got.tone != want.tone || (got.address != "") != want.copy {
					t.Errorf("estado %d, LAN %v: renglón %d %+v", c.state, showLAN, i, got)
				}
				if want.copy && got.address != network.public.String() {
					t.Errorf("estado %d: copia %q, quería la IP pública %s", c.state, got.address, network.public)
				}
				if got.showLog != (got.tone == toneError) {
					t.Errorf("estado %d: renglón %d %+v: solo los rojos muestran el registro", c.state, i, got)
				}
			}
			wantDirect := c.direct && !showLAN
			if got := copiesDirectly(options); got != wantDirect {
				t.Errorf("estado %d, LAN %v: copia directo = %v", c.state, showLAN, got)
			}

			var lanRows, hints int
			for _, option := range options[len(c.internet):] {
				switch {
				case option.label == i18n.T("invite.lan_hint"):
					hints++
					if option.address != "" || option.tone != toneMuted || option.showLog {
						t.Errorf("estado %d: la sugerencia de LAN/VPN %+v se puede tocar o no es gris", c.state, option)
					}
				case option.address != "":
					lanRows++
				default:
					t.Errorf("estado %d: renglón de más %+v", c.state, option)
				}
			}
			for _, option := range options {
				local := strings.HasPrefix(option.address, "192.168.") || strings.HasPrefix(option.address, "26.")
				if local && !showLAN {
					t.Errorf("estado %d sin LAN: ofrece la dirección local %s", c.state, option.address)
				}
			}
			if showLAN {
				if lanRows != len(testInterfaces) || hints != 0 {
					t.Errorf("estado %d con LAN: %d LAN/VPN y %d sugerencias, quería %d y 0", c.state, lanRows, hints, len(testInterfaces))
				}
				continue
			}
			wantHints := 0
			if c.lanHint {
				wantHints = 1
			}
			if lanRows != 0 || hints != wantHints {
				t.Errorf("estado %d sin LAN: %+v", c.state, options)
			}
		}
	}
}

// Mientras se busca, el renglón naranja dice lo mismo que la barra de estado y el registro; con el
// puerto sin abrir, el rojo manda al registro de actividad.
func TestInviteOptionsTexts(t *testing.T) {
	finding := inviteOptions(testNetwork(hostNetFinding), nil, testListenPort, false)[0]
	if finding.label != hostNetMsg(testNetwork(hostNetFinding)).String() {
		t.Errorf("buscando: %q, quería el texto de la barra de estado", finding.label)
	}
	noUPnP := inviteOptions(testNetwork(hostNetNoUPnP), nil, testListenPort, false)
	if noUPnP[0].label != i18n.T("invite.port_failed") {
		t.Errorf("puerto sin abrir: primer renglón %q", noUPnP[0].label)
	}
	if noUPnP[1].label != i18n.T("invite.internet_port_closed", "181.10.20.30:47831") {
		t.Errorf("puerto sin abrir: segundo renglón %q", noUPnP[1].label)
	}
}

// internetOption es el primer renglón que se puede copiar (el de internet, en los tests sin LAN).
func internetOption(options []inviteOption) inviteOption {
	for _, option := range options {
		if option.address != "" {
			return option
		}
	}
	return inviteOption{}
}

// Al copiar: el aviso normal, o el del puerto que falta abrir (en el router, o también en el módem).
func TestInviteOptionsCopyNotice(t *testing.T) {
	cases := []struct {
		state hostNetState
		key   string
	}{
		{hostNetReady, "invite.copied"},
		{hostNetNoUPnP, "invite.copied_port_closed"},
		{hostNetDoubleNAT, "invite.copied_double_nat"},
	}
	for _, c := range cases {
		first := internetOption(inviteOptions(testNetwork(c.state), nil, testListenPort, false))
		if first.copied.Key != c.key || len(first.copied.Args) == 0 || first.copied.Args[0] != first.address {
			t.Errorf("estado %d: aviso %s %v, quería %s con %s", c.state, first.copied.Key, first.copied.Args, c.key, first.address)
		}
		if c.key != "invite.copied" && (len(first.copied.Args) != 2 || first.copied.Args[1] != uint16(47831)) {
			t.Errorf("estado %d: aviso %v sin el puerto externo 47831", c.state, first.copied.Args)
		}
	}
	ready := inviteOptions(testNetwork(hostNetReady), testInterfaces, testListenPort, true)
	lan := ready[1]
	if lan.copied.Key != "invite.copied" {
		t.Fatalf("renglón LAN: aviso %s", lan.copied.Key)
	}
	if lan.address != "192.168.1.37:47830" || lan.label != i18n.T("invite.lan", "Ethernet 4", "192.168.1.37:47830") {
		t.Fatalf("renglón LAN %+v: quería la IP de la interfaz con el puerto del socket", lan)
	}
}

// Estado → texto del registro de actividad y de la barra de estado (el mismo en los dos).
func TestHostNetMsg(t *testing.T) {
	cases := []struct {
		network hostNetwork
		key     string
		args    []any
	}{
		{testNetwork(hostNetFinding), "conn.net_finding", nil},
		{testNetwork(hostNetReady), "conn.net_ready", nil},
		{testNetwork(hostNetNoUPnP), "conn.net_no_upnp", []any{uint16(47831), "192.168.1.37"}},
		{hostNetwork{state: hostNetNoUPnP, public: netip.MustParseAddrPort("181.10.20.30:47830")}, "conn.net_no_upnp", []any{uint16(47830), "?"}},
		{testNetwork(hostNetNoIP), "conn.net_no_ip", nil},
		{testNetwork(hostNetCGNAT), "conn.net_cgnat", nil},
		{testNetwork(hostNetDoubleNAT), "conn.net_double_nat", []any{"192.168.0.10", uint16(47831)}},
	}
	for _, c := range cases {
		msg := hostNetMsg(c.network)
		if msg.Key != c.key || len(msg.Args) != len(c.args) {
			t.Errorf("estado %d: %s %v, quería %s %v", c.network.state, msg.Key, msg.Args, c.key, c.args)
			continue
		}
		for i := range c.args {
			if msg.Args[i] != c.args[i] {
				t.Errorf("estado %d: arg %d = %v, quería %v", c.network.state, i, msg.Args[i], c.args[i])
			}
		}
		for _, lang := range []i18n.Lang{i18n.ES, i18n.EN} {
			if text := msg.In(lang); strings.Contains(text, "%!") {
				t.Errorf("estado %d en %s: %q", c.network.state, lang, text)
			}
		}
	}
}

// Inicio + duración → hasta cuándo queda abierto el puerto: hoy, o mañana si pasa la medianoche
// (en la hora local de cuando se abrió).
func TestPortLeaseMsg(t *testing.T) {
	zone := time.FixedZone("UTC-3", -3*3600)
	cases := []struct {
		opened time.Time
		hours  int
		key    string
		clock  string
	}{
		{time.Date(2026, 11, 13, 9, 15, 0, 0, zone), 8, "conn.port_until", "17:15"},
		{time.Date(2026, 11, 13, 21, 34, 0, 0, zone), 8, "conn.port_until_tomorrow", "05:34"},
		{time.Date(2026, 11, 13, 15, 59, 30, 0, zone), 8, "conn.port_until", "23:59"},
		{time.Date(2026, 11, 13, 16, 0, 0, 0, zone), 8, "conn.port_until_tomorrow", "00:00"},
		{time.Date(2026, 11, 13, 0, 0, 0, 0, zone), 24, "conn.port_until_tomorrow", "00:00"},
		{time.Date(2026, 12, 31, 22, 0, 0, 0, zone), 1, "conn.port_until", "23:00"},
		{time.Date(2026, 12, 31, 23, 30, 0, 0, zone), 1, "conn.port_until_tomorrow", "00:30"},
	}
	for _, c := range cases {
		msg := portLeaseMsg(c.opened, time.Duration(c.hours)*time.Hour)
		if msg.Key != c.key || len(msg.Args) != 2 || msg.Args[0] != c.clock || msg.Args[1] != c.hours {
			t.Errorf("%s + %d h: %s %v, quería %s [%s %d]", c.opened.Format("02/01 15:04"), c.hours, msg.Key, msg.Args, c.key, c.clock, c.hours)
		}
		for _, lang := range []i18n.Lang{i18n.ES, i18n.EN} {
			if text := msg.In(lang); strings.Contains(text, "%!") || !strings.Contains(text, c.clock) {
				t.Errorf("%s en %s: %q", c.key, lang, text)
			}
		}
	}
	expired := portExpiredMsg(8 * time.Hour)
	if expired.Key != "conn.port_expired" || len(expired.Args) != 1 || expired.Args[0] != 8 {
		t.Errorf("vencido: %s %v", expired.Key, expired.Args)
	}
	for _, lang := range []i18n.Lang{i18n.ES, i18n.EN} {
		if text := expired.In(lang); strings.Contains(text, "%!") || !strings.Contains(text, "8 h") {
			t.Errorf("vencido en %s: %q", lang, text)
		}
	}
}

// Con el puerto de UPnP, la línea del resultado suma hasta cuándo queda abierto; sin regla, no.
func TestHostNetMsgWithLease(t *testing.T) {
	opened := time.Date(2026, 11, 13, 21, 34, 0, 0, time.Local)
	for _, state := range []hostNetState{hostNetReady, hostNetDoubleNAT} {
		network := testNetwork(state)
		network.portOpened, network.portLease = opened, 8*time.Hour
		for _, lang := range []i18n.Lang{i18n.ES, i18n.EN} {
			want := hostNetStateMsg(network).In(lang) + " " + portLeaseMsg(opened, 8*time.Hour).In(lang)
			if got := hostNetMsg(network).In(lang); got != want {
				t.Errorf("estado %d en %s: %q, quería %q", state, lang, got, want)
			}
		}
	}
	if got, want := hostNetMsg(testNetwork(hostNetReady)).In(i18n.ES), i18n.M("conn.net_ready").In(i18n.ES); got != want {
		t.Errorf("sin regla: %q, quería %q", got, want)
	}
}

// IP externa del router (UPnP) → estado: CGNAT no deja entrar, privada = router detrás de otro, lo
// demás (pública aunque sea otra, sin respuesta) queda listo.
func TestRouterWANState(t *testing.T) {
	cases := []struct {
		wan  string
		want hostNetState
	}{
		{"100.64.0.1", hostNetCGNAT},
		{"100.127.255.254", hostNetCGNAT},
		{"192.168.0.10", hostNetDoubleNAT},
		{"10.0.0.2", hostNetDoubleNAT},
		{"172.16.5.4", hostNetDoubleNAT},
		{"::ffff:192.168.0.10", hostNetDoubleNAT},
		{"181.10.20.30", hostNetReady},
		{"190.1.2.3", hostNetReady}, // otra pública: solo se loguea
		{"0.0.0.0", hostNetReady},   // algunos routers la contestan con el WAN caído
	}
	for _, c := range cases {
		if got := routerWANState(netip.MustParseAddr(c.wan)); got != c.want {
			t.Errorf("%s: estado %d, quería %d", c.wan, got, c.want)
		}
	}
	if got := routerWANState(netip.Addr{}); got != hostNetReady {
		t.Errorf("sin respuesta: estado %d", got)
	}
}
