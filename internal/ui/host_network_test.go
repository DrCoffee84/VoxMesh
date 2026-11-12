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
	if state == hostNetReady || state == hostNetNoUPnP {
		n.public = netip.MustParseAddrPort("181.10.20.30:47831")
	}
	if state == hostNetNoUPnP {
		n.localIP = netip.MustParseAddr("192.168.1.37")
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

// Con el puerto sin abrir, copiar avisa qué puerto falta abrir.
func TestInviteOptionsPortClosed(t *testing.T) {
	options := inviteOptions(testNetwork(hostNetNoUPnP), nil, testListenPort, false)
	if options[0].closedPort != 47831 {
		t.Fatalf("closedPort = %d, quería el puerto externo 47831", options[0].closedPort)
	}
	ready := inviteOptions(testNetwork(hostNetReady), testInterfaces, testListenPort, true)
	for _, option := range ready {
		if option.closedPort != 0 {
			t.Fatalf("listo: %+v avisaría de un puerto cerrado", option)
		}
	}
	lan := ready[1]
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
