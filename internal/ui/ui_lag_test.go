//go:build cgo

package ui

import (
	"encoding/binary"
	"image"
	"image/color"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"voxmesh/internal/room"
	"voxmesh/internal/transport"
)

const (
	lagHostID = "00000000000000aa"
	lagSelfID = "00000000000000bb"
	lagPepeID = "00000000000000cc"
)

// lagClient arma un cliente en una sala con el host Coffee (anunciado con su IP pública) y Pepe.
// fo es la migración ya conectada al host en hostAddr (la dirección que marcó el invitado).
func lagClient(t *testing.T, hostAddr string) (*App, *room.Failover) {
	t.Helper()
	test.NewTempApp(t)
	a := New(filepath.Join(t.TempDir(), "voxmesh.exe"))
	a.participantID = lagSelfID
	a.roomState = room.State{Name: "sala", Epoch: 1, HostID: lagHostID, Participants: []room.Participant{
		{ID: lagHostID, Username: "Coffee", Address: "9.9.9.9:47830", Order: 0, Connected: true},
		{ID: lagSelfID, Username: "Yo", Address: "[::]:47830", Order: 1, Connected: true},
		{ID: lagPepeID, Username: "Pepe", Address: "8.8.8.8:47830", Order: 2, Connected: true},
	}}
	a.refreshPeersFromRoom()
	fo := room.NewFailover(lagSelfID, room.DefaultFailoverTimings())
	fo.JoinedAsClient(hostAddr, time.Now())
	return a, fo
}

func peerNamed(a *App, name string) peerView {
	a.peerMu.RLock()
	defer a.peerMu.RUnlock()
	for _, peer := range a.peerNames {
		if peer.name == name {
			return peer
		}
	}
	return peerView{}
}

func udpAddr(t *testing.T, address string) *net.UDPAddr {
	t.Helper()
	addr, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		t.Fatal(err)
	}
	return addr
}

// TestPeerLagsOnlyFromHost (C18): el lag que manda el host va al chip de los demás (con "≈") y no al
// historial; el del host y el propio no se tocan. De otro que no es el host no se acepta, y uno mal
// formado se descarta. Un mensaje sin Pepe lo devuelve a "… ms".
func TestPeerLagsOnlyFromHost(t *testing.T) {
	const hostAddr = "192.168.1.5:47830"
	a, fo := lagClient(t, hostAddr)
	payload, err := room.EncodePeerLags([]room.PeerLag{{ID: lagPepeID, MS: 150}, {ID: lagHostID, MS: 5}, {ID: lagSelfID, MS: 9}})
	if err != nil {
		t.Fatal(err)
	}
	send := func(from string, payload []byte) {
		packet := transport.Packet{Kind: transport.PacketPeerLag, Payload: payload}
		a.handleClientPacket(nil, fo, packet, udpAddr(t, from), from, time.Now())
	}

	send("8.8.8.8:47830", payload) // Pepe no es el host
	if pepe := peerNamed(a, "Pepe"); pepe.lag != 0 {
		t.Fatalf("aceptó el lag de alguien que no es el host: %v", pepe.lag)
	}

	send(hostAddr, payload)
	pepe := peerNamed(a, "Pepe")
	if pepe.lag != 150*time.Millisecond || !pepe.lagViaHost {
		t.Fatalf("Pepe = %v (por el host: %v), quería 150 ms del host", pepe.lag, pepe.lagViaHost)
	}
	if text, _ := formatPeerLag(pepe); !strings.Contains(text, "≈150 ms") {
		t.Fatalf("chip de Pepe %q: quería «≈150 ms»", text)
	}
	if host := peerNamed(a, "Coffee"); host.lag != 0 {
		t.Fatalf("el lag del host lo mide esta PC, no el mensaje: %v", host.lag)
	}
	if a.lagHistory.version("Pepe") != 0 {
		t.Fatal("el número del host no va al historial")
	}

	send(hostAddr, []byte{1, 2, 3}) // roto: se descarta y no cambia nada
	if peerNamed(a, "Pepe").lag != 150*time.Millisecond {
		t.Fatal("un mensaje roto cambió el lag")
	}

	empty, _ := room.EncodePeerLags(nil)
	send(hostAddr, empty)
	if pepe := peerNamed(a, "Pepe"); pepe.lag != 0 || pepe.lagViaHost {
		t.Fatalf("sin número fresco Pepe sigue con %v", pepe.lag)
	}
}

// TestClientPongFromHostByID (C18): el host se anuncia con su IP pública pero el invitado entró por
// LAN, así que el Pong llega de otra dirección. Va igual al chip y al historial del host, por su ID.
// El Pong de otro (pings de la migración) no.
func TestClientPongFromHostByID(t *testing.T) {
	const hostAddr = "192.168.1.5:47830"
	a, fo := lagClient(t, hostAddr)
	pong := func(from string) {
		payload := make([]byte, 8)
		binary.BigEndian.PutUint64(payload, uint64(time.Now().Add(-30*time.Millisecond).UnixNano()))
		packet := transport.Packet{Kind: transport.PacketPong, Payload: payload}
		a.handleClientPacket(nil, fo, packet, udpAddr(t, from), from, time.Now())
	}

	pong("8.8.8.8:47830")
	if peerNamed(a, "Pepe").lag != 0 || a.lagHistory.version("Pepe") != 0 {
		t.Fatal("el Pong de Pepe (no es el host) se guardó")
	}
	pong(hostAddr)
	host := peerNamed(a, "Coffee")
	if host.lag < 30*time.Millisecond || host.lagViaHost {
		t.Fatalf("lag del host = %v (por el host: %v), quería ~30 ms medido acá", host.lag, host.lagViaHost)
	}
	if points := a.lagHistory.points("Coffee", time.Now().Add(-time.Minute), time.Now().Add(time.Minute)); len(points) != 1 || points[0].rtt < 30 {
		t.Fatalf("historial del host %+v, quería una muestra de ~30 ms", points)
	}
}

// TestPeerLagsToSend (host): solo lo que midió esta PC y fresco.
func TestPeerLagsToSend(t *testing.T) {
	test.NewTempApp(t)
	a := New(filepath.Join(t.TempDir(), "voxmesh.exe"))
	now := time.Now()
	a.peerNames = []peerView{
		{id: lagHostID, name: "Yo", isSelf: true, lag: time.Millisecond, lagAt: now},
		{id: "0000000000000001", name: "Fresco", lag: 80 * time.Millisecond, lagAt: now.Add(-2 * time.Second)},
		{id: "0000000000000002", name: "Viejo", lag: 80 * time.Millisecond, lagAt: now.Add(-peerLagFresh - time.Second)},
		{id: "0000000000000003", name: "Del host viejo", lag: 80 * time.Millisecond, lagAt: now, lagViaHost: true},
		{id: "0000000000000004", name: "Sin medir"},
	}
	lags := a.peerLagsToSend(now)
	if len(lags) != 1 || lags[0].ID != "0000000000000001" || lags[0].MS != 80 {
		t.Fatalf("manda %+v, quería solo el fresco con 80 ms", lags)
	}
}

// TestLagToggleShowsStrips (C18): las tiras arrancan ocultas; el ícono las muestra, cambia la
// leyenda y lo guarda en la config; de nuevo, las oculta. El chip propio no tiene tira.
func TestLagToggleShowsStrips(t *testing.T) {
	test.NewTempApp(t).Settings().SetTheme(voxTheme{})
	a := New(filepath.Join(t.TempDir(), "voxmesh.exe"))
	setLanguage(t, "es") // después de New, que aplica el de la config
	window := test.NewTempWindow(t, a.content())
	window.Resize(fyne.NewSize(960, 700))
	a.peerNames = []peerView{{id: lagSelfID, name: "Yo", isSelf: true}, {id: lagPepeID, name: "Pepe"}}
	a.refreshPeerAvatars()
	if len(a.peerChips) != 2 || a.peerChips[0].strip != nil || a.peerChips[1].strip == nil {
		t.Fatalf("chips %+v: quería tira solo en el de Pepe", a.peerChips)
	}
	strip := a.peerChips[1].strip
	if strip.Visible() || a.lagToggle.hint != "Mostrar detalle de lag" {
		t.Fatalf("al arrancar: tira visible=%v, leyenda %q", strip.Visible(), a.lagToggle.hint)
	}

	test.Tap(a.lagToggle)
	if !a.conf().ShowLagGraph || !strip.Visible() || a.lagToggle.hint != "Ocultar detalle de lag" {
		t.Fatalf("después de tocar: config=%v, tira visible=%v, leyenda %q", a.conf().ShowLagGraph, strip.Visible(), a.lagToggle.hint)
	}
	if size := strip.Size(); size.Width != lagStripWidth || size.Height != lagStripHeight {
		t.Fatalf("la tira mide %v, quería %dx%d (el ancho del slider)", size, lagStripWidth, lagStripHeight)
	}
	// Un chip nuevo (otro participante) ya sale con la tira a la vista.
	a.peerNames = append(a.peerNames, peerView{id: lagHostID, name: "Coffee"})
	a.refreshPeerAvatars()
	if !a.peerChips[2].strip.Visible() {
		t.Fatal("el chip nuevo salió sin la tira")
	}

	test.Tap(a.lagToggle)
	if a.conf().ShowLagGraph || a.peerChips[1].strip.Visible() {
		t.Fatal("el segundo toque no las ocultó")
	}
}

func TestLagPointText(t *testing.T) {
	setLanguage(t, "es")
	when := time.Date(2026, 11, 13, 21, 34, 12, 0, time.Local)
	point := lagPoint{at: when, rtt: 350, loss: 8, jitter: 6}
	if got := lagPointText(point, time.Time{}, false); got != "21:34:12 · 350 ms · 8 % de pérdida" {
		t.Errorf("tira: %q", got)
	}
	if got := lagPointText(point, time.Time{}, true); got != "21:34:12 · 350 ms · 8 % de pérdida · ±6 ms" {
		t.Errorf("gráfico grande: %q", got)
	}
	if got := lagPointText(lagPoint{at: when, rtt: -1, loss: 3, jitter: -1}, time.Time{}, true); got != "21:34:12 · 3 % de pérdida" {
		t.Errorf("solo pérdida: %q", got)
	}
	if got := lagPointText(emptyLagPoint(), when, false); got != "21:34:12 · sin datos" {
		t.Errorf("hueco: %q", got)
	}
	setLanguage(t, "en")
	if got := lagPointText(point, time.Time{}, false); got != "21:34:12 · 350 ms · 8 % loss" {
		t.Errorf("en: %q", got)
	}
	if got := lagSummaryText(lagSummary{maxRTT: 600, avgRTT: 120, maxLoss: 12}); got != "Max 600 ms · average 120 ms · max loss 12 %" {
		t.Errorf("resumen en: %q", got)
	}
	setLanguage(t, "es")
	if got := lagSummaryText(lagSummary{maxRTT: -1, avgRTT: -1, maxLoss: 4}); got != "Pérdida máxima 4 %" {
		t.Errorf("resumen solo pérdida: %q", got)
	}
}

// TestDrawLagStrip: la barrita va con el color del semáforo, la pérdida es una marca arriba y un
// hueco queda con el fondo.
func TestDrawLagStrip(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 3, lagStripHeight))
	drawLagStrip(img, []lagPoint{
		{at: time.Now(), rtt: 450, loss: 8, jitter: -1},
		emptyLagPoint(),
		{at: time.Now(), rtt: 30, loss: 0, jitter: -1},
	})
	same := func(got color.Color, want color.Color) bool {
		r1, g1, b1, _ := got.RGBA()
		r2, g2, b2, _ := want.RGBA()
		return r1 == r2 && g1 == g2 && b1 == b2
	}
	if !same(img.At(0, lagStripHeight-1), lagRed) || !same(img.At(0, 0), lagRed) {
		t.Error("450 ms con pérdida: quería barra roja y marca arriba")
	}
	if !same(img.At(2, lagStripHeight-1), speakingColor()) {
		t.Error("30 ms: quería la barrita verde")
	}
	if _, _, _, alpha := img.At(2, 0).RGBA(); alpha != uint32(lagStripBackground.A)*0x101 {
		t.Error("30 ms: la barra no llega arriba (raíz cuadrada de 30/500 ≈ un cuarto)")
	}
	if img.At(1, lagStripHeight-1) != img.At(2, 0) {
		t.Error("el hueco tiene que quedar con el fondo")
	}
}
