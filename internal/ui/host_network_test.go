//go:build cgo

package ui

import (
	"net"
	"net/netip"
	"strings"
	"testing"

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

// Cada estado de la red del host × el check de LAN/VPN: el primer renglón dice cómo está internet;
// sin el check nunca aparece una IP local, y LAN/VPN se sugiere (en gris) solo cuando internet
// falló, nunca mientras se espera.
func TestInviteOptions(t *testing.T) {
	type want struct {
		first     inviteTone
		copyFirst bool // el renglón de internet se puede copiar
		lanHint   bool
		direct    bool // "Invitar" copia sin menú
	}
	cases := []struct {
		state hostNetState
		want  want
	}{
		{hostNetFinding, want{first: toneWarning}},
		{hostNetReady, want{first: toneNormal, copyFirst: true, direct: true}},
		{hostNetNoUPnP, want{first: toneWarning, copyFirst: true, lanHint: true}},
		{hostNetNoIP, want{first: toneError, lanHint: true}},
		{hostNetCGNAT, want{first: toneError, lanHint: true}},
		{hostNetDoubleNAT, want{first: toneWarning, copyFirst: true, lanHint: true}},
	}
	for _, c := range cases {
		for _, showLAN := range []bool{false, true} {
			network := testNetwork(c.state)
			options := inviteOptions(network, testInterfaces, testListenPort, showLAN)
			if len(options) == 0 {
				t.Fatalf("estado %d, LAN %v: sin renglones", c.state, showLAN)
			}
			first := options[0]
			if first.tone != c.want.first || (first.address != "") != c.want.copyFirst {
				t.Errorf("estado %d, LAN %v: primer renglón %+v", c.state, showLAN, first)
			}
			if c.want.copyFirst && first.address != network.public.String() {
				t.Errorf("estado %d: copia %q, quería la IP pública %s", c.state, first.address, network.public)
			}
			wantDirect := c.want.direct && !showLAN
			if got := copiesDirectly(options); got != wantDirect {
				t.Errorf("estado %d, LAN %v: copia directo = %v", c.state, showLAN, got)
			}

			var lanRows, hints int
			for _, option := range options[1:] {
				switch {
				case option.label == i18n.T("invite.lan_hint"):
					hints++
					if option.address != "" || option.tone != toneMuted {
						t.Errorf("estado %d: la sugerencia de LAN/VPN %+v se puede copiar o no es gris", c.state, option)
					}
				case option.address != "":
					lanRows++
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
			if c.want.lanHint {
				wantHints = 1
			}
			if lanRows != 0 || hints != wantHints || len(options) != 1+wantHints {
				t.Errorf("estado %d sin LAN: %+v", c.state, options)
			}
		}
	}
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
		first := inviteOptions(testNetwork(c.state), nil, testListenPort, false)[0]
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
