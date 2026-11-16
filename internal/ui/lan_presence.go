//go:build cgo

package ui

import (
	"errors"
	"net"
	"net/netip"
	"sync"
	"time"

	"fyne.io/fyne/v2"

	"voxmesh/internal/audio"
	"voxmesh/internal/history"
	"voxmesh/internal/i18n"
	"voxmesh/internal/invite"
	"voxmesh/internal/logging"
	"voxmesh/internal/netinfo"
	"voxmesh/internal/presence"
	"voxmesh/internal/room"
)

// "En tu red" (AGENDA F9): el servicio de internal/presence corre mientras el check "Aparecer en mi
// red" (lan_presence_enabled, Ajustes → Red) esté prendido. lanPresenceLoop sigue la config en vivo,
// así un cambio en el borrador de Ajustes se aplica al toque y "Descartar" lo deja como estaba sin
// nada más. La tarjeta (ui_lan.go) lee lanSnapshot.

type lanState int

const (
	lanOff    lanState = iota // check apagado (o todavía no arrancó)
	lanOn                     // avisando y escuchando
	lanFailed                 // el puerto no se pudo abrir: se reintenta cada lanRetry
)

const (
	lanPoll    = 500 * time.Millisecond // cada cuánto se mira el check
	lanRetry   = 10 * time.Second
	lanLogFail = time.Minute // la falla al abrir va al log a lo sumo una vez por minuto
	// lanFirewallAfter: con una interfaz de Radmin y nada escuchado por ella en este tiempo, el
	// registro de actividad sugiere el firewall (una vez por sesión).
	lanFirewallAfter = time.Minute
)

// lanPresence es el estado del servicio, con su mutex. Nunca se llama a Service.Stop con el mutex
// tomado: Stop espera a las goroutines de lectura, que pueden estar avisando un cambio
// (refreshLANCard) y leer lanSnapshot.
type lanPresence struct {
	mu         sync.Mutex
	service    *presence.Service
	state      lanState
	closed     bool // la app se está cerrando: no se vuelve a prender
	failedAt   time.Time
	failLogged time.Time
	testConfig func(*presence.Config) // solo los tests: escuchar en 127.0.0.1 y avisar adonde digan
	startedAt  time.Time              // cuándo arrancó el servicio (lanFirewallAfter)
	firewall   bool                   // ya se miró si hacía falta el aviso del firewall (una vez por sesión)
}

// lanPresenceLoop prende y apaga el servicio según el check, hasta que la app se cierra. Va en su
// goroutine (Run).
func (a *App) lanPresenceLoop() {
	ticker := time.NewTicker(lanPoll)
	defer ticker.Stop()
	for a.syncLANPresence(time.Now()) {
		a.checkLANFirewall(time.Now())
		<-ticker.C
	}
}

// checkLANFirewall: Windows suele marcar la red de Radmin como "Pública" y, si al permitir VoxMesh
// solo se marcó "Privada", los avisos de los demás no entran. No se puede saber seguro (puede que
// nadie de Radmin tenga la app abierta): si hay una interfaz de Radmin y en lanFirewallAfter no
// llegó nada por ella, una línea en el registro de actividad dice qué revisar. Una vez por sesión.
func (a *App) checkLANFirewall(now time.Time) {
	l := &a.lan
	l.mu.Lock()
	service := l.service
	due := service != nil && !l.firewall && now.Sub(l.startedAt) >= lanFirewallAfter
	if due {
		l.firewall = true
	}
	l.mu.Unlock()
	if !due || service.HeardFromRadmin() {
		return
	}
	interfaces, _ := netinfo.LocalIPv4s()
	for _, iface := range interfaces {
		if ip, ok := netip.AddrFromSlice(iface.IP.To4()); ok && netinfo.IsRadmin(ip) {
			msg := i18n.M("lan.firewall_hint")
			logging.Infof("Registro de actividad: %s", msg.In(i18n.ES))
			a.addActivity(msg.String())
			return
		}
	}
}

// syncLANPresence deja el servicio como dice la config. false = la app se está cerrando.
func (a *App) syncLANPresence(now time.Time) bool {
	want := a.conf().LANPresence
	l := &a.lan
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return false
	}
	switch {
	case want && l.service == nil && (l.state != lanFailed || now.Sub(l.failedAt) >= lanRetry):
		config := presence.Config{
			Port:     presence.Port,
			Targets:  presence.InterfaceTargets(presence.Port),
			Status:   a.lanStatus,
			OnChange: a.refreshLANCard,
			OnCall:   a.onCallEvent,
		}
		if l.testConfig != nil {
			l.testConfig(&config)
		}
		service, err := presence.Start(config)
		if err != nil {
			l.state, l.failedAt = lanFailed, now
			if now.Sub(l.failLogged) >= lanLogFail {
				l.failLogged = now
				logging.Errorf("Red local: no se pudo abrir el puerto UDP %d: %s", presence.Port, logging.FormatError(err))
			}
		} else {
			l.service, l.state, l.startedAt = service, lanOn, now
		}
		l.mu.Unlock()
	case !want && l.state != lanOff:
		service := l.service
		l.service, l.state = nil, lanOff
		l.mu.Unlock()
		if service != nil {
			service.Stop() // "me voy": desaparece de la lista de los demás al toque
		}
	default:
		l.mu.Unlock()
		return true
	}
	a.refreshLANCard()
	return true
}

// stopLANPresence apaga el servicio para siempre (al cerrar la app: quit). Manda el "me voy".
func (a *App) stopLANPresence() {
	l := &a.lan
	l.mu.Lock()
	l.closed = true
	service := l.service
	l.service, l.state = nil, lanOff
	l.mu.Unlock()
	if service != nil {
		service.Stop()
	}
}

// lanStatus es lo que dice el aviso propio: el nombre (como en la sala) y si estoy en una sala (o
// entrando a una). Lo lee el servicio cada 250 ms, desde su goroutine.
func (a *App) lanStatus() presence.Status {
	name := room.ClampUsername(a.conf().Username)
	if name == "" {
		name = i18n.T("default.username")
	}
	return presence.Status{Name: name, InRoom: a.connected()}
}

// lanSnapshot es el estado del servicio y quién está en la red ahora.
func (a *App) lanSnapshot() (lanState, []presence.Peer) {
	l := &a.lan
	l.mu.Lock()
	state, service := l.state, l.service
	l.mu.Unlock()
	if service == nil {
		return state, nil
	}
	return state, service.Peers()
}

// lanService es el servicio de ahora (nil = apagado).
func (a *App) lanService() *presence.Service {
	a.lan.mu.Lock()
	defer a.lan.mu.Unlock()
	return a.lan.service
}

// callRoomName es la sala que abre "Llamar" fuera de una sala: la elegida en "Ver otra sala…" del
// chat y, si no hay, la última (cfg.RoomName).
func callRoomName(viewing, last string) string {
	if name := history.ClampRoomName(viewing); name != "" {
		return name
	}
	return history.ClampRoomName(last)
}

// callPeer es el botón Llamar (hilo de UI). Fuera de una sala, primero abre una (callRoomName) y
// recién con el socket abierto sale el "te llamo": la invitación se arma cuando atiende.
func (a *App) callPeer(peer presence.Peer) {
	service := a.lanService()
	if service == nil || a.callUI != nil {
		return
	}
	seq := a.callSeq.Add(1)
	a.showOutgoingCall(peer.Name)
	name := a.lanStatus().Name
	connected := a.connected()
	roomName := a.activeRoomName
	if !connected {
		roomName = callRoomName(a.viewingRoomName, a.conf().RoomName)
		if roomName == "" {
			roomName = i18n.T("default.room_name")
		}
	}
	go func() {
		if !connected {
			a.openHost(roomName, name)
			if !a.waitForRoom(5 * time.Second) {
				fyne.Do(func() { a.finishOutgoingCall(seq, i18n.T("lan.no_room")) })
				return
			}
		}
		if a.callSeq.Load() != seq {
			return // cortó mientras se abría la sala
		}
		if _, err := service.Call(peer.Addr, name, roomName); err != nil {
			logging.Infof("Red local: no se pudo llamar a %s: %s", peer.Addr, err)
			fyne.Do(func() { a.finishOutgoingCall(seq, i18n.T("lan.call_failed", err.Error())) })
			return
		}
		logging.Infof("Red local: llamando a %q (%s), sala %q", peer.Name, peer.Addr, roomName)
	}()
}

// waitForRoom espera a que la sala que se está abriendo tenga su socket.
func (a *App) waitForRoom(limit time.Duration) bool {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if a.transport.Load() != nil {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// hangUpCall es Cortar (hilo de UI).
func (a *App) hangUpCall() {
	a.callSeq.Add(1)
	if service := a.lanService(); service != nil {
		service.CancelCall()
	}
	a.closeCallUI()
}

// answerCall es Atender (true) o Rechazar (hilo de UI).
func (a *App) answerCall(accept bool) {
	a.stopRing()
	if service := a.lanService(); service != nil {
		service.Answer(accept)
	}
	if !accept {
		a.closeCallUI()
		return
	}
	a.setCallText(i18n.T("lan.joining"))
}

// onCallEvent llega desde el servicio. Si el llamado atendió, manda la invitación ahí mismo (nunca
// al log ni a la barra de estado: lleva la clave); lo demás va al cartel, en el hilo de UI.
func (a *App) onCallEvent(event presence.CallEvent) {
	if event.Kind == presence.CallAccepted {
		code, err := a.callInvite(event.Peer)
		if err == nil {
			if service := a.lanService(); service != nil {
				err = service.SendInvite(event.Call, code)
			}
		}
		if err != nil {
			logging.Infof("Red local: atendió %s pero no salió la invitación: %s", event.Peer, err)
			fyne.Do(func() { a.finishOutgoingCall(a.callSeq.Load(), i18n.T("lan.no_room")) })
			return
		}
		logging.Infof("Red local: %s atendió, invitación enviada", event.Peer)
	}
	fyne.Do(func() { a.showCallEvent(event) })
}

var errNoRoom = errors.New("no hay sala abierta")

// callInvite es la invitación para quien atendió desde peer: con la IP de esta PC que llega a él
// (por Radmin, la 26.x; nunca la pública ni la de la ruta por defecto) si soy el host, o la del
// host como la veo yo si soy cliente.
func (a *App) callInvite(peer netip.AddrPort) (string, error) {
	udp := a.transport.Load()
	if udp == nil {
		return "", errNoRoom
	}
	var addr netip.AddrPort
	if a.hostMode.Load() {
		ip, err := localIPToward(peer.Addr())
		if err != nil {
			return "", err
		}
		addr = netip.AddrPortFrom(ip, uint16(udp.Address().Port))
	} else {
		var err error
		if addr, err = netip.ParseAddrPort(a.endpointText()); err != nil {
			return "", err
		}
	}
	return invite.Format(addr, udp.Key()), nil
}

// localIPToward es la IP de esta PC por la que sale un paquete hacia ip (la de la interfaz de esa
// red). Un "connect" de UDP no manda nada.
func localIPToward(ip netip.Addr) (netip.Addr, error) {
	conn, err := net.DialUDP("udp4", nil, net.UDPAddrFromAddrPort(netip.AddrPortFrom(ip, presence.Port)))
	if err != nil {
		return netip.Addr{}, err
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).AddrPort().Addr().Unmap(), nil
}

// ring hace sonar el timbre cada 2 s hasta stopRing. Suena siempre, aunque los sonidos de eventos
// estén apagados o esté ensordecido: con la app en la bandeja es el aviso de verdad.
func (a *App) ring() {
	gen := a.ringGen.Add(1)
	go func() {
		for a.ringGen.Load() == gen {
			a.playCue(audio.SFXRing)
			time.Sleep(2 * time.Second)
		}
	}()
}

func (a *App) stopRing() { a.ringGen.Add(1) }
