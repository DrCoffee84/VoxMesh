//go:build cgo

package ui

import (
	"net/netip"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"voxmesh/internal/config"
	"voxmesh/internal/history"
	"voxmesh/internal/i18n"
	"voxmesh/internal/identity"
	"voxmesh/internal/invite"
	"voxmesh/internal/presence"
	"voxmesh/internal/room"
	"voxmesh/internal/transport"
)

// joiner es alguien de la red que pide entrar: un servicio de presencia suelto en su propia IP de
// loopback (127.0.0.x), así el tope de un pedido por IP cada 10 s no frena el test.
type joiner struct {
	*presence.Service
	events chan presence.JoinEvent
}

func startJoiner(t *testing.T, ip string, name string, id *identity.Identity, targets ...netip.AddrPort) *joiner {
	t.Helper()
	j := &joiner{events: make(chan presence.JoinEvent, 8)}
	s, err := presence.Start(presence.Config{
		ListenIP: netip.MustParseAddr(ip),
		Targets:  func() []netip.AddrPort { return targets },
		Interval: 100 * time.Millisecond,
		Status:   func() presence.Status { return presence.Status{Name: name} },
		OnJoin:   func(e presence.JoinEvent) { j.events <- e },
		Identity: id,
	})
	if err != nil {
		t.Fatal(err)
	}
	j.Service = s
	t.Cleanup(s.Stop)
	return j
}

func (j *joiner) next(t *testing.T, kind presence.JoinEventKind) presence.JoinEvent {
	t.Helper()
	select {
	case e := <-j.events:
		if e.Kind != kind {
			t.Fatalf("evento %v, quería %v (%+v)", e.Kind, kind, e)
		}
		return e
	case <-time.After(3 * time.Second):
		t.Fatalf("no llegó el evento %v", kind)
	}
	return presence.JoinEvent{}
}

// knocksNow son los pedidos pendientes, leídos en el hilo de UI.
func knocksNow(a *App) []knock {
	var out []knock
	fyne.DoAndWait(func() { out = append(out, a.knocks...) })
	return out
}

// TestJoinRoomFromLAN (F10): pedir entrar a la sala de alguien de la red, de punta a punta por
// loopback. Sin confianza queda pendiente (suena, cartel, bandeja) y entra al aceptar; con "Dejar
// entrar siempre" la próxima vez entra solo, sin cartel ni sonido; Rechazar; Aceptar desde la
// bandeja no lo suma a la lista; vencido, el cartel se va.
func TestJoinRoomFromLAN(t *testing.T) {
	test.NewTempApp(t).Settings().SetTheme(voxTheme{})
	t.Setenv("AppData", t.TempDir())
	var knocked atomic.Int32
	savedCue := knockCue
	knockCue = func(*App) { knocked.Add(1) }
	t.Cleanup(func() { knockCue = savedCue })

	// La dueña de la sala: host de "Mi sala", con "En tu red" por loopback.
	b := New(filepath.Join(t.TempDir(), "voxmesh.exe"))
	b.updateConfig(func(c *config.Config) { c.Username, c.EventSoundsEnabled = "Coffee", false })
	b.window = test.NewTempWindow(t, b.content())
	root, _ := config.AppDir()
	store, err := history.New(root, "Mi sala")
	if err != nil {
		t.Fatal(err)
	}
	b.historyStore.Store(store)
	b.trust.open(store)
	key, err := transport.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	udp, err := transport.Listen("127.0.0.1:0", key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { udp.Close() })
	b.transport.Store(udp)
	b.hostMode.Store(true)
	b.participantID = "host"
	b.roomState = room.State{Name: "Mi sala", Epoch: 1, HostID: "host", Participants: []room.Participant{{ID: "host", Username: "Coffee", Connected: true}}}

	juanID := identity.New()
	scout := startJoiner(t, "127.0.0.2", "Juan", juanID)
	var targets atomic.Pointer[[]netip.AddrPort]
	targets.Store(&[]netip.AddrPort{netip.AddrPortFrom(netip.MustParseAddr("127.0.0.2"), uint16(scout.ListenPort()))})
	b.lan.testConfig = func(c *presence.Config) {
		c.ListenIP = netip.MustParseAddr("127.0.0.1")
		c.Port = 0
		c.Targets = func() []netip.AddrPort { return *targets.Load() }
		c.Interval = 100 * time.Millisecond
	}
	b.syncLANPresence(time.Now())
	t.Cleanup(b.stopLANPresence)

	// El aviso dice la sala: así aparece "en «Mi sala»" con Entrar.
	var owner presence.Peer
	waitFor(t, "el aviso de Coffee con la sala", func() bool {
		for _, peer := range scout.Peers() {
			if peer.Name == "Coffee" && peer.InRoom && peer.Room == "Mi sala" {
				owner = peer
				return true
			}
		}
		return false
	})

	// 1. Juan no está en la lista: queda pendiente, suena, cartel y bandeja. Nadie le contesta todavía.
	if _, err := scout.Join(owner.Addr, "Juan"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "el pedido de Juan queda pendiente", func() bool { return len(knocksNow(b)) == 1 })
	if knocked.Load() != 1 {
		t.Errorf("sonó %d veces", knocked.Load())
	}
	var row fyne.CanvasObject
	var trayItems []*fyne.MenuItem
	fyne.DoAndWait(func() {
		row = b.knockBox.Objects[0]
		trayItems = b.trayMenuItems()
	})
	if len(labelsIn(row)) == 0 || labelsIn(row)[0].Text != i18n.T("lan.knock", "Juan") {
		t.Errorf("cartel: %+v", labelsIn(row))
	}
	// En la sala (la columna izquierda oculta, como siempre que hay pedidos) entra en la ventana de
	// fábrica.
	b.window.Resize(fyne.NewSize(config.DefaultWindowWidth, config.DefaultWindowHeight))
	fyne.DoAndWait(b.hideConnectionPanels)
	t.Logf("alto mínimo en la sala con un pedido: %v", b.window.Content().MinSize().Height)
	fitsDefaultWindow(t, b.window, "con un pedido para entrar")
	saveShot(t, b.window, "knock_banner")
	fyne.DoAndWait(b.showConnectionPanels) // ☰ dentro de la sala
	fitsDefaultWindow(t, b.window, "con ☰ y un pedido para entrar")
	if width := b.window.Content().MinSize().Width; width > config.DefaultWindowWidth {
		t.Errorf("con ☰ y un pedido la ventana necesita %v de ancho", width)
	}
	saveShot(t, b.window, "knock_banner_panels")
	if item := trayItems[1]; item.Label != i18n.T("lan.knock", "Juan") || item.ChildMenu == nil || len(item.ChildMenu.Items) != 2 {
		t.Errorf("bandeja: %+v", item)
	}
	select {
	case e := <-scout.events:
		t.Fatalf("pendiente: no tenía que llegarle nada todavía (%+v)", e)
	case <-time.After(200 * time.Millisecond):
	}
	// Aceptar con "Dejar entrar siempre".
	fyne.DoAndWait(func() {
		findInTree(row, checkWithText(i18n.T("lan.knock_always"))).(*widget.Check).SetChecked(true)
		test.Tap(findInTree(row, buttonWithText(i18n.T("lan.accept"))).(*widget.Button))
	})
	admitted := scout.next(t, presence.JoinAdmitted)
	code, err := invite.Parse(admitted.Invite)
	if err != nil || code.Key != key || int(code.Addr.Port()) != udp.Address().Port {
		t.Fatalf("invitación: %v %+v", err, code)
	}
	waitFor(t, "Juan queda en la lista de la sala", func() bool { return b.trust.has(juanID.Public()) })
	// addTrusted escribe el estado de la sala justo después (la misma goroutine).
	waitFor(t, "el estado de la sala lleva a Juan", func() bool { return room.TrustedHas(b.roomState.Trusted, juanID.Public()) })
	waitFor(t, "la lista se guarda en confianza.json", func() bool {
		data, _ := store.ReadTrust()
		list := room.DecodeTrustFile(data)
		return len(list) == 1 && list[0].Name == "Juan" && list[0].By == "Coffee"
	})
	if len(knocksNow(b)) != 0 {
		t.Error("el cartel sigue")
	}

	// 2. Juan otra vez (desde otra IP de loopback, con su misma llave): entra solo.
	again := startJoiner(t, "127.0.0.3", "Juan", juanID)
	again.Join(owner.Addr, "Juan")
	again.next(t, presence.JoinAdmitted)
	if len(knocksNow(b)) != 0 || knocked.Load() != 1 {
		t.Errorf("de confianza: sin cartel ni sonido (%d pedidos, sonó %d)", len(knocksNow(b)), knocked.Load())
	}

	// 3. Pedro y Ana a la vez: el cartel muestra uno y cuántos más; la bandeja, los dos. Pedro:
	// Rechazar.
	pedroID, anaID := identity.New(), identity.New()
	pedro := startJoiner(t, "127.0.0.4", "Pedro", pedroID)
	pedro.Join(owner.Addr, "Pedro")
	waitFor(t, "el pedido de Pedro", func() bool { return len(knocksNow(b)) == 1 })
	ana := startJoiner(t, "127.0.0.5", "Ana", anaID)
	ana.Join(owner.Addr, "Ana")
	waitFor(t, "el pedido de Ana", func() bool { return len(knocksNow(b)) == 2 })
	fyne.DoAndWait(func() {
		if len(b.knockBox.Objects) != 2 || labelsIn(b.knockBox.Objects[1])[0].Text != i18n.T("lan.knock_more", 1) {
			t.Errorf("con dos: un cartel y \"+1\": %d objetos", len(b.knockBox.Objects))
		}
		if items := b.trayMenuItems(); items[1].Label != i18n.T("lan.knock", "Pedro") || items[2].Label != i18n.T("lan.knock", "Ana") {
			t.Errorf("bandeja con dos: %q %q", items[1].Label, items[2].Label)
		}
		fitsDefaultWindow(t, b.window, "con dos pedidos")
		test.Tap(findInTree(b.knockBox.Objects[0], buttonWithText(i18n.T("lan.reject"))).(*widget.Button))
	})
	if got := pedro.next(t, presence.JoinDeclined); got.Reason != presence.ReasonRejected {
		t.Errorf("rechazo: %+v", got)
	}
	if b.trust.has(pedroID.Public()) {
		t.Error("rechazado y en la lista")
	}

	// 4. Ana: Aceptar desde la bandeja (sin el check): entra, pero no queda en la lista.
	waitFor(t, "queda el pedido de Ana", func() bool { return len(knocksNow(b)) == 1 })
	fyne.DoAndWait(func() {
		if len(b.knockBox.Objects) != 1 || labelsIn(b.knockBox.Objects[0])[0].Text != i18n.T("lan.knock", "Ana") {
			t.Error("contestado Pedro, el cartel pasa a Ana")
		}
		b.trayMenuItems()[1].ChildMenu.Items[0].Action()
	})
	ana.next(t, presence.JoinAdmitted)
	time.Sleep(100 * time.Millisecond)
	if b.trust.has(anaID.Public()) {
		t.Error("aceptada sin el check y en la lista")
	}

	// 5. Luis: vence sin respuesta (el servicio avisa JoinExpired a los 2 min): el cartel se va.
	luis := startJoiner(t, "127.0.0.6", "Luis", identity.New())
	luis.Join(owner.Addr, "Luis")
	waitFor(t, "el pedido de Luis", func() bool { return len(knocksNow(b)) == 1 })
	join := knocksNow(b)[0].join
	b.onJoinEvent(presence.JoinEvent{Kind: presence.JoinExpired, Join: join, Name: "Luis", Reason: presence.ReasonNoAnswer})
	waitFor(t, "el pedido vencido sale del cartel", func() bool { return len(knocksNow(b)) == 0 })
	var trayAfter []*fyne.MenuItem
	fyne.DoAndWait(func() { trayAfter = b.trayMenuItems() })
	for _, item := range trayAfter {
		if strings.Contains(item.Label, "Luis") {
			t.Error("el pedido vencido sigue en la bandeja")
		}
	}
}

// TestJoinRequesterDialog (F10): al que pide, "Esperando que Coffee te deje entrar… [Cancelar]" y
// después el resultado ("no te dejó entrar", "Nadie contestó").
func TestJoinRequesterDialog(t *testing.T) {
	test.NewTempApp(t).Settings().SetTheme(voxTheme{})
	t.Setenv("AppData", t.TempDir())
	savedCue := knockCue
	knockCue = func(*App) {}
	t.Cleanup(func() { knockCue = savedCue })

	r := New(filepath.Join(t.TempDir(), "voxmesh.exe"))
	r.updateConfig(func(c *config.Config) { c.Username = "Juan" })
	r.window = test.NewTempWindow(t, r.content())
	r.lan.testConfig = func(c *presence.Config) {
		c.ListenIP = netip.MustParseAddr("127.0.0.8")
		c.Port = 0
		c.Targets = func() []netip.AddrPort { return nil }
	}
	r.syncLANPresence(time.Now())
	t.Cleanup(r.stopLANPresence)

	// Coffee ya salió de su sala (su estado dice libre): contesta "no está en una sala". Su
	// dirección directa sale de su aviso.
	startJoiner(t, "127.0.0.7", "Coffee", identity.New(), netip.AddrPortFrom(netip.MustParseAddr("127.0.0.8"), uint16(r.lanService().ListenPort())))
	var peer presence.Peer
	waitFor(t, "el aviso de Coffee", func() bool {
		for _, p := range r.lanService().Peers() {
			if p.Name == "Coffee" {
				peer = p
				return true
			}
		}
		return false
	})
	peer.InRoom, peer.Room = true, "Mi sala" // como lo vio un momento antes
	fyne.DoAndWait(func() { r.joinPeer(peer) })
	var text string
	fyne.DoAndWait(func() {
		if r.callUI != nil {
			text = r.callUI.text.Text
		}
	})
	if text != i18n.T("lan.join_waiting", "Coffee") {
		t.Fatalf("cartel: %q", text)
	}
	waitFor(t, "el resultado en el cartel", func() bool {
		var got string
		fyne.DoAndWait(func() { got = r.callUI.text.Text })
		return got == i18n.T("lan.join_no_room", "Coffee")
	})

	// Los demás resultados, armados a mano.
	for _, c := range []struct {
		reason presence.Reason
		want   string
	}{
		{presence.ReasonRejected, i18n.T("lan.join_rejected", "Coffee")},
		{presence.ReasonNoAnswer, i18n.T("lan.join_no_answer")},
		{presence.ReasonBusy, i18n.T("lan.join_busy", "Coffee")},
	} {
		fyne.DoAndWait(func() {
			r.closeCallUI()
			r.callSeq.Add(1)
			ui := r.openCallUI(i18n.T("lan.join_title"), "Coffee", i18n.T("lan.join_waiting", "Coffee"))
			ui.seq, ui.joining = r.callSeq.Load(), true
			r.showJoinResult(presence.JoinEvent{Kind: presence.JoinDeclined, Reason: c.reason})
			text = r.callUI.text.Text
		})
		if text != c.want {
			t.Errorf("motivo %d: %q, quería %q", c.reason, text, c.want)
		}
	}
	// Cancelar cierra el cartel.
	fyne.DoAndWait(func() {
		r.closeCallUI()
		r.joinPeer(peer)
		test.Tap(findInTree(r.callUI.buttons, buttonWithText(i18n.T("common.cancel"))).(*widget.Button))
		if r.callUI != nil {
			t.Error("Cancelar no cerró el cartel")
		}
	})
}
