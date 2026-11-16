package presence

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sync"
	"syscall"
	"time"

	"voxmesh/internal/logging"
	"voxmesh/internal/netinfo"
)

const (
	AnnounceInterval = 3 * time.Second // cada cuánto se repite el aviso
	statusPoll       = 250 * time.Millisecond
	rejectLogEvery   = 10 * time.Second // lo descartado va al log a lo sumo una línea por este tiempo
)

// Status es lo que dice el aviso propio: el nombre y si estoy en una sala.
type Status struct {
	Name   string
	InRoom bool
}

// Config es cómo arranca el servicio. La app usa Port, todas las interfaces y LANTargets; los
// tests escuchan en 127.0.0.1 con puertos libres y se pasan las direcciones entre ellos.
type Config struct {
	Port     int                     // donde escucha los avisos (Port; 0 = uno libre)
	ListenIP netip.Addr              // vacía = todas las interfaces
	Targets  func() []netip.AddrPort // adónde va el aviso; se vuelve a pedir en cada aviso periódico
	Status   func() Status           // se lee cada 250 ms: un cambio sale en el momento
	OnChange func()                  // cambió la lista; otra goroutine, nunca con un lock tomado
	OnCall   func(CallEvent)         // algo pasó con una llamada (calls.go); otra goroutine
	Interval time.Duration           // 0 = AnnounceInterval
	TTL      time.Duration           // 0 = PeerTTL
}

// Service avisa y escucha en la red. Tiene dos sockets: listen, en el puerto fijo y compartido
// (SO_REUSEADDR), donde llegan los broadcasts de todos; y direct, en un puerto libre, que manda
// todo y recibe lo que viene directo (llamadas, respuestas). Con SO_REUSEADDR el broadcast le llega
// a todas las instancias de una PC, pero un paquete directo al puerto fijo le llegaría a una sola,
// cualquiera: por eso lo directo va al puerto propio de cada una (la dirección de su aviso).
type Service struct {
	cfg    Config
	self   ID
	listen *net.UDPConn
	direct *net.UDPConn
	roster *Roster

	stop     chan struct{}
	stopOnce sync.Once
	announce sync.WaitGroup
	receive  sync.WaitGroup

	callMu      sync.Mutex
	out         *outCall                 // la llamada que hago (una a la vez)
	in          *inCall                  // la que me hacen (una a la vez)
	callFrom    map[netip.Addr]time.Time // última llamada por IP (CallCooldown)
	heardRadmin bool                     // llegó un aviso por Radmin (HeardFromRadmin)

	rejectMu     sync.Mutex
	rejected     uint64 // en total (los tests lo miran)
	rejectSince  uint64 // desde la última línea del log
	rejectLogged time.Time
}

// Start abre los dos sockets, manda el primer aviso y queda escuchando hasta Stop.
func Start(cfg Config) (*Service, error) {
	if cfg.Interval <= 0 {
		cfg.Interval = AnnounceInterval
	}
	if cfg.TTL <= 0 {
		cfg.TTL = PeerTTL
	}
	ip := cfg.ListenIP
	if !ip.IsValid() {
		ip = netip.IPv4Unspecified()
	}
	listen, err := listenUDP(netip.AddrPortFrom(ip, uint16(cfg.Port)), true)
	if err != nil {
		return nil, err
	}
	direct, err := listenUDP(netip.AddrPortFrom(ip, 0), false)
	if err != nil {
		_ = listen.Close()
		return nil, err
	}
	self := NewID()
	s := &Service{
		cfg:    cfg,
		self:   self,
		listen: listen,
		direct: direct,
		roster: NewRoster(self, cfg.TTL),
		stop:   make(chan struct{}),

		callFrom: map[netip.Addr]time.Time{},
	}
	s.receive.Add(2)
	go s.receiveLoop(listen)
	go s.receiveLoop(direct)
	s.announce.Add(1)
	go s.announceLoop()
	logging.Infof("Red local: avisando y escuchando en el puerto %d (directo en el %d)", s.ListenPort(), direct.LocalAddr().(*net.UDPAddr).Port)
	return s, nil
}

// Stop manda "me voy" (así desaparece de la lista de los demás ya, sin esperar los 10 s) y cierra
// los sockets. Se puede llamar más de una vez.
func (s *Service) Stop() {
	s.stopOnce.Do(func() {
		close(s.stop)
		s.CancelCall()
		s.announce.Wait() // el "me voy" sale de announceLoop
		_ = s.listen.Close()
		_ = s.direct.Close()
		s.receive.Wait()
		logging.Infof("Red local: apagado")
	})
}

// ID es el de esta sesión (va en cada aviso).
func (s *Service) ID() ID { return s.self }

// ListenPort es el puerto donde escucha los avisos (Port en la app).
func (s *Service) ListenPort() int { return s.listen.LocalAddr().(*net.UDPAddr).Port }

// Peers es quién está en la red ahora, en el orden de Roster.List.
func (s *Service) Peers() []Peer { return s.roster.List() }

func (s *Service) announceLoop() {
	defer s.announce.Done()
	targets := s.cfg.Targets()
	last := s.cfg.Status()
	s.sendAll(Message{Kind: KindPresence, From: s.self, Name: last.Name, InRoom: last.InRoom}, targets)
	lastSent := time.Now()
	ticker := time.NewTicker(statusPoll)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			s.sendAll(Message{Kind: KindBye, From: s.self}, targets)
			return
		case now := <-ticker.C:
			status := s.cfg.Status()
			periodic := now.Sub(lastSent) >= s.cfg.Interval
			if periodic {
				targets = s.cfg.Targets() // una interfaz nueva (Radmin se conectó) entra acá
			}
			if periodic || status != last {
				s.sendAll(Message{Kind: KindPresence, From: s.self, Name: status.Name, InRoom: status.InRoom}, targets)
				last, lastSent = status, now
			}
			if s.roster.Expire(now) {
				s.changed()
			}
			s.checkCalls(now)
		}
	}
}

// sendAll manda m a cada destino desde el socket directo (así las respuestas vuelven a él).
func (s *Service) sendAll(m Message, targets []netip.AddrPort) {
	packet, err := Encode(m)
	if err != nil {
		return // sin nombre: no hay qué avisar
	}
	for _, target := range targets {
		if _, err := s.direct.WriteToUDPAddrPort(packet, target); err != nil && !errors.Is(err, net.ErrClosed) {
			logging.Tracef("Red local: no salió el aviso a %s: %s", target, err)
		}
	}
}

func (s *Service) receiveLoop(conn *net.UDPConn) {
	defer s.receive.Done()
	buf := make([]byte, MaxPacket+1) // uno de más: lo que no entra es demasiado grande
	for {
		n, from, err := conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			if messageTooLong(err) {
				s.reject(from, ErrTooLarge, time.Now()) // Windows no lo corta: falla la lectura
				continue
			}
			logging.Tracef("Red local: leer: %s", err)
			select {
			case <-s.stop:
				return
			case <-time.After(50 * time.Millisecond):
			}
			continue
		}
		s.handle(buf[:n], netip.AddrPortFrom(from.Addr().Unmap(), from.Port()), time.Now())
	}
}

// handle procesa un paquete que llegó. Nada se ejecuta ni se guarda: solo cambia la lista en
// memoria.
func (s *Service) handle(data []byte, from netip.AddrPort, now time.Time) {
	if !LocalSource(from.Addr()) {
		s.reject(from, errInternetSource, now)
		return
	}
	m, err := Decode(data)
	if err != nil {
		s.reject(from, err, now)
		return
	}
	if m.From == s.self {
		return // el aviso propio, que vuelve por el broadcast
	}
	switch m.Kind {
	case KindPresence:
		s.markHeard(from.Addr())
		if s.roster.Seen(m.From, m.Name, m.InRoom, from, now) {
			s.changed()
		}
	case KindBye:
		if s.roster.Bye(m.From, from) {
			s.changed()
		}
	default:
		s.handleCall(m, from, now)
	}
}

func (s *Service) markHeard(ip netip.Addr) {
	if !netinfo.IsRadmin(ip) {
		return
	}
	s.callMu.Lock()
	s.heardRadmin = true
	s.callMu.Unlock()
}

// HeardFromRadmin dice si llegó algún aviso por Radmin (26.0.0.0/8) desde que arrancó: si hay una
// interfaz de Radmin y nunca llegó nada, puede ser el firewall (Radmin suele quedar como red
// "Pública").
func (s *Service) HeardFromRadmin() bool {
	s.callMu.Lock()
	defer s.callMu.Unlock()
	return s.heardRadmin
}

var errInternetSource = errors.New("viene de una IP de internet")

// LocalSource dice si se acepta un paquete que viene de ip: solo de la misma red o VPN (privadas,
// CGNAT/Tailscale, Radmin, loopback). De una IP de internet nunca: los broadcasts no pasan routers,
// así que un paquete de ahí es de alguien que apunta al puerto a propósito.
func LocalSource(ip netip.Addr) bool {
	return ip.IsValid() && !netinfo.IsInternet(ip)
}

// reject cuenta un paquete descartado y lo anota en el log, a lo sumo una línea cada 10 s. Nunca
// el contenido: solo de dónde vino y por qué.
func (s *Service) reject(from netip.AddrPort, why error, now time.Time) {
	s.rejectMu.Lock()
	s.rejected++
	s.rejectSince++
	if now.Sub(s.rejectLogged) < rejectLogEvery {
		s.rejectMu.Unlock()
		return
	}
	count := s.rejectSince
	s.rejectSince, s.rejectLogged = 0, now
	s.rejectMu.Unlock()
	logging.Infof("Red local: %d paquetes descartados (último de %s: %s)", count, from, why)
}

func (s *Service) changed() {
	if s.cfg.OnChange != nil {
		s.cfg.OnChange()
	}
}

// LANTargets son las direcciones de broadcast de las interfaces de la misma red o VPN, con el
// puerto port: 192.168.1.255, el 26.255.255.255 de Radmin… Sin las que tienen IP de internet (el
// aviso nunca sale para afuera) ni las que no tienen broadcast (/31, /32: Tailscale). Sin repetir.
func LANTargets(interfaces []netinfo.InterfaceIP, port int) []netip.AddrPort {
	var targets []netip.AddrPort
	seen := map[netip.Addr]bool{}
	for _, iface := range interfaces {
		ip, ok := netip.AddrFromSlice(iface.IP.To4())
		if !ok || netinfo.IsInternet(ip) {
			continue
		}
		broadcast, ok := netinfo.Broadcast(iface.IP, iface.Mask)
		if !ok || seen[broadcast] {
			continue
		}
		seen[broadcast] = true
		targets = append(targets, netip.AddrPortFrom(broadcast, uint16(port)))
	}
	return targets
}

// InterfaceTargets es Config.Targets para la app: LANTargets de las interfaces de ahora.
func InterfaceTargets(port int) func() []netip.AddrPort {
	var logged time.Time
	return func() []netip.AddrPort {
		interfaces, err := netinfo.LocalIPv4s()
		if err != nil && time.Since(logged) >= time.Minute {
			logged = time.Now()
			logging.Errorf("Red local: listar interfaces: %s", logging.FormatError(err))
		}
		return LANTargets(interfaces, port)
	}
}

// listenUDP abre un socket UDP IPv4 en addr con broadcast; shared (el puerto fijo) además con
// SO_REUSEADDR, para que dos instancias en la misma PC escuchen los avisos.
func listenUDP(addr netip.AddrPort, shared bool) (*net.UDPConn, error) {
	config := net.ListenConfig{
		Control: func(_, _ string, raw syscall.RawConn) error {
			var optErr error
			if err := raw.Control(func(fd uintptr) { optErr = setSocketOptions(fd, shared) }); err != nil {
				return err
			}
			return optErr
		},
	}
	conn, err := config.ListenPacket(context.Background(), "udp4", addr.String())
	if err != nil {
		return nil, err
	}
	return conn.(*net.UDPConn), nil
}
