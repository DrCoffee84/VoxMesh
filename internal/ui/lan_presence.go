//go:build cgo

package ui

import (
	"sync"
	"time"

	"voxmesh/internal/i18n"
	"voxmesh/internal/logging"
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
}

// lanPresenceLoop prende y apaga el servicio según el check, hasta que la app se cierra. Va en su
// goroutine (Run).
func (a *App) lanPresenceLoop() {
	ticker := time.NewTicker(lanPoll)
	defer ticker.Stop()
	for a.syncLANPresence(time.Now()) {
		<-ticker.C
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
			l.service, l.state = service, lanOn
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
