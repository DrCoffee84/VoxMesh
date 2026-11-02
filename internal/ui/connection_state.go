//go:build cgo

package ui

import (
	"net"
	"sync/atomic"

	"voxmesh/internal/audio"
	"voxmesh/internal/transport"
)

// Estado de la conexión que comparten la UI, receiveLoop y los callbacks de malgo. Cada pieza
// es un puntero atómico a un valor inmutable: los lectores hacen una sola carga (sin locks) y
// los cambios publican un valor nuevo entero. Regla: cargar UNA vez y usar la variable local;
// `if a.x != nil { a.x.Foo() }` con dos lecturas puede ver nil en la segunda.

// stopSignal es el canal que detiene un grupo de goroutines (las de la conexión, o las del rol de
// host). start crea uno nuevo (y cierra el anterior); stop lo cierra una sola vez aunque lo pidan a
// la vez dos goroutines (bajarse de host y cortar la sala).
type stopSignal struct {
	ch atomic.Pointer[chan struct{}]
}

func (s *stopSignal) start() <-chan struct{} {
	ch := make(chan struct{})
	if previous := s.ch.Swap(&ch); previous != nil {
		close(*previous)
	}
	return ch
}

func (s *stopSignal) stop() {
	if previous := s.ch.Swap(nil); previous != nil {
		close(*previous)
	}
}

// endpointInfo es la dirección de la sala: la del host (cliente) o la que se comparte (host).
// addr se resuelve al publicarla para que el audio saliente no llame a ResolveUDPAddr por frame.
type endpointInfo struct {
	text string
	addr *net.UDPAddr // nil si text no es una dirección UDP utilizable
}

// connected informa si hay una sala o conexión activa (a.transport publicado).
func (a *App) connected() bool {
	return a.transport.Load() != nil
}

func (a *App) endpointText() string {
	if info := a.endpoint.Load(); info != nil {
		return info.text
	}
	return ""
}

func (a *App) endpointAddr() *net.UDPAddr {
	if info := a.endpoint.Load(); info != nil {
		return info.addr
	}
	return nil
}

// setEndpoint publica la dirección de la sala. text tiene que ser "IP:puerto" literal (todos los
// llamadores ya la tienen resuelta): así ResolveUDPAddr no hace DNS. "" la borra.
func (a *App) setEndpoint(text string) {
	if text == "" {
		a.endpoint.Store(nil)
		return
	}
	info := &endpointInfo{text: text}
	if addr, err := net.ResolveUDPAddr("udp", text); err == nil && addr.IP != nil && !addr.IP.IsUnspecified() {
		info.addr = addr
	}
	a.endpoint.Store(info)
}

// engine devuelve el Engine de la sala o nil.
func (a *App) engine() *audio.Engine {
	return a.audioEngine.Load()
}

// stopEngine despublica el Engine y lo detiene. Swap garantiza que dos llamadas concurrentes
// (cortar la sala mientras se reinicia el audio) no lo detengan dos veces.
func (a *App) stopEngine() {
	if engine := a.audioEngine.Swap(nil); engine != nil {
		engine.Stop()
	}
}

// publishEngine publica un Engine recién abierto para la conexión udp y detiene el anterior.
// Abrir malgo tarda cientos de ms: si en el medio se cortó la conexión, el Engine nuevo se
// detiene en vez de quedar capturando el micrófono sin sala. Devuelve si quedó publicado.
func (a *App) publishEngine(engine *audio.Engine, udp *transport.UDP) bool {
	if a.transport.Load() != udp {
		engine.Stop()
		return false
	}
	if previous := a.audioEngine.Swap(engine); previous != nil {
		previous.Stop()
	}
	// stopConnectionAsync suelta el transporte antes que el Engine. Si el corte cayó entre el
	// chequeo de arriba y el Swap, lo saca quien llegue primero (su Swap o este CAS), una vez.
	if a.transport.Load() != udp && a.audioEngine.CompareAndSwap(engine, nil) {
		engine.Stop()
		return false
	}
	return true
}
