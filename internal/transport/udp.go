package transport

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	magic              = "VM02"
	HeaderSize         = 16
	PacketAudio        = 1
	PacketPing         = 3
	PacketPong         = 4
	PacketRoomHello    = 5
	PacketRoomState    = 6
	PacketChat         = 7
	PacketChatSync     = 8
	PacketAssetChunk   = 9
	PacketAssetMissing = 10
	PacketRoomLeave    = 11
	PacketRoomMute     = 12 // cliente → host: cambio de mute propio; el host responde con PacketRoomState
	// Pantalla compartida. Share y Watch: cliente → host, el host responde con PacketRoomState
	// (Participant.Sharing / Watching). Tile: un chunk de un tile PNG, de quien comparte al host y
	// del host a los que miran (internal/screen).
	PacketScreenShare = 13
	PacketScreenWatch = 14
	PacketScreenTile  = 15
	// ScreenRate: host → cliente que comparte, el ritmo que puede usar (LAN o internet, entre las
	// copias que reenvía el host). Cursor: posición del cursor de quien comparte, sin cola (chico y
	// urgente). Digest: resumen de los tiles enviados (quien mira pide lo que le falta). Repair:
	// quien mira → host → quien comparte, los tiles que tiene viejos.
	PacketScreenRate   = 16
	PacketScreenCursor = 17
	PacketScreenDigest = 18
	PacketScreenRepair = 19
)

func PacketKindName(kind byte) string {
	switch kind {
	case PacketAudio:
		return "Audio"
	case PacketPing:
		return "Ping"
	case PacketPong:
		return "Pong"
	case PacketRoomHello:
		return "RoomHello"
	case PacketRoomState:
		return "RoomState"
	case PacketChat:
		return "Chat"
	case PacketChatSync:
		return "ChatSync"
	case PacketAssetChunk:
		return "AssetChunk"
	case PacketAssetMissing:
		return "AssetMissing"
	case PacketRoomLeave:
		return "RoomLeave"
	case PacketRoomMute:
		return "RoomMute"
	case PacketScreenShare:
		return "ScreenShare"
	case PacketScreenWatch:
		return "ScreenWatch"
	case PacketScreenTile:
		return "ScreenTile"
	case PacketScreenRate:
		return "ScreenRate"
	case PacketScreenCursor:
		return "ScreenCursor"
	case PacketScreenDigest:
		return "ScreenDigest"
	case PacketScreenRepair:
		return "ScreenRepair"
	default:
		return fmt.Sprintf("Unknown(%d)", kind)
	}
}

// FlagAudioSequence indica que Sequence es la secuencia de audio propia del emisor:
// contigua, solo avanza con paquetes de audio. Viaja en el byte 5 del header; sin el flag,
// Sequence es el contador global.
const FlagAudioSequence byte = 1 << 0

// FlagShout marca un paquete de audio que salió en modo grito: los demás le ponen un aro rojo
// al avatar del emisor. Solo informativo (quien no lo conoce lo ignora).
const FlagShout byte = 1 << 1

// SocketBufferBytes es el tamaño pedido para los buffers de envío y recepción del socket:
// absorbe ráfagas (reenvío del host, sincronización de assets) sin que el SO descarte audio.
const SocketBufferBytes = 4 << 20

// Ritmo de los archivos (chunks de sonidos e imágenes, SendBulk). Sin límite, mandar un sonido de
// 8 MB salía a ~20 Mbps por destino: saturaba la subida del host varios segundos, la cola del
// router demoraba también la voz que reenvía y en Wi-Fi le quitaba aire al audio. La voz y el
// control (Send, Broadcast) no pasan por acá.
const (
	BulkRateBytes  = 250_000 // ~2 Mbps en total, sumando todos los destinos
	bulkBurstBytes = 16_000
	bulkQueue      = 256 // paquetes esperando turno
)

// Ritmo de la pantalla compartida (SendScreen, TryRelayScreen): una cola aparte de la de archivos,
// así una sincronización de sonidos no frena la pantalla ni al revés. ScreenRateBytes es el tope de
// lo que sale de este socket sumando destinos por internet: el host que reenvía a varios no pasa de
// ahí. En una LAN (todos con IP privada y ping bajo, lo decide el host) el tope es
// ScreenLANRateBytes. Ráfaga chica (5 ms de datos, mínimo unos 5 paquetes) y cola corta: el ritmo
// parejo deja vacía la cola del router y la voz, que no pasa por acá, no espera detrás de una imagen.
const (
	ScreenRateBytes    = 187_500   // ~1,5 Mbps en total: internet
	ScreenLANRateBytes = 2_500_000 // ~20 Mbps en total: LAN
	screenBurstBytes   = 6_000
	screenQueue        = 32
	screenMinRate      = 1_000
)

type Packet struct {
	Kind     byte
	Flags    byte
	Sequence uint32
	// Talkspurt (byte 6) es el número de frase del audio: el emisor lo incrementa cada vez que
	// vuelve a mandar después de un silencio (compuerta cerrada, mute). El receptor lo usa para
	// no medir el silencio como retardo de red (secuencia contigua, llegada muy posterior).
	Talkspurt byte
	Payload   []byte

	// sealed son los bytes tal como llegaron (Receive): el host los reenvía sin volver a cifrar
	// (Relay, TryRelayBulk).
	sealed []byte
}

// MaxPayloadSize es el payload más grande que entra en un datagrama sellado.
const MaxPayloadSize = maxDatagram - HeaderSize - SealOverhead

// sealBuffers son los buffers de Send/Broadcast: sellar no asigna memoria por paquete (audio).
var sealBuffers = sync.Pool{New: func() any {
	buf := make([]byte, 0, 2048)
	return &buf
}}

// UDP guarda los peers como una lista copy-on-write: Broadcast (reenvío del host, audio
// saliente) y el AddPeer de cada paquete recibido leen la instantánea con una carga atómica,
// sin lock ni asignaciones. Solo los cambios reales de la lista toman peersMu, que serializa a
// los escritores entre sí y nunca a un lector.
//
// Todo lo que entra y sale de la sala pasa por acá sellado con la clave de la sala (AES-256-GCM,
// crypto.go): lo que no abre se descarta en Receive y se cuenta en Invalid.
type UDP struct {
	conn        *net.UDPConn
	key         Key
	cipher      *roomCipher
	peersMu     sync.Mutex
	peers       atomic.Pointer[[]*net.UDPAddr] // inmutable una vez publicada
	readBuf     []byte                         // buffer de lectura de Receive (un solo lector: receiveLoop)
	invalid     atomic.Uint64                  // datagramas descartados por Receive (no abren)
	lastInvalid atomic.Pointer[net.UDPAddr]    // origen del último descartado (para el log)

	bulk      *lane // archivos (SendBulk, BroadcastBulk, TryRelayBulk)
	screen    *lane // pantalla compartida (SendScreen, TryRelayScreen)
	closed    chan struct{}
	closeOnce sync.Once
}

// laneJob es un paquete con ritmo para un destino, para targets o para todos los peers menos
// except. Si sealed no es nil, sale tal cual (reenvío del host).
type laneJob struct {
	packet    Packet
	sealed    []byte
	to        *net.UDPAddr
	targets   []*net.UDPAddr // solo lectura: el llamador no la modifica después
	except    *net.UDPAddr
	broadcast bool
}

// lane es una cola con ritmo propio (token bucket) y su goroutine, que arranca con el primer
// paquete. La tasa se puede cambiar en vivo (SetScreenRate).
type lane struct {
	jobs  chan laneJob
	rate  atomic.Int64 // bytes por segundo
	burst int
	once  sync.Once

	sentBytes atomic.Uint64 // bytes escritos al socket, sumando destinos
	dropped   atomic.Uint64 // paquetes descartados por cola llena (Try*)
}

func newLane(rate, burst, queue int) *lane {
	l := &lane{jobs: make(chan laneJob, queue), burst: burst}
	l.rate.Store(int64(rate))
	return l
}

// Listen abre el socket de la sala con su clave.
func Listen(address string, key Key) (*UDP, error) {
	sealer, err := newRoomCipher(key)
	if err != nil {
		return nil, err
	}
	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			var controlErr error
			err := c.Control(func(fd uintptr) {
				controlErr = configureSocket(fd)
			})
			if err != nil {
				return err
			}
			return controlErr
		},
	}
	packetConn, err := lc.ListenPacket(context.Background(), "udp", address)
	if err != nil {
		return nil, err
	}
	conn, ok := packetConn.(*net.UDPConn)
	if !ok {
		_ = packetConn.Close()
		return nil, fmt.Errorf("no es una conexión UDP válida")
	}
	// Best effort: el SO puede limitar el tamaño; con el default (64 KB en Windows) una ráfaga
	// de chunks de assets alcanzaba para que se descarte audio.
	_ = conn.SetReadBuffer(SocketBufferBytes)
	_ = conn.SetWriteBuffer(SocketBufferBytes)
	return &UDP{
		conn:    conn,
		key:     key,
		cipher:  sealer,
		readBuf: make([]byte, 64*1024),
		bulk:    newLane(BulkRateBytes, bulkBurstBytes, bulkQueue),
		screen:  newLane(ScreenRateBytes, screenBurstBytes, screenQueue),
		closed:  make(chan struct{}),
	}, nil
}

func (u *UDP) Address() *net.UDPAddr { return u.conn.LocalAddr().(*net.UDPAddr) }

// Key es la clave de la sala de este socket (para armar la invitación).
func (u *UDP) Key() Key { return u.key }

// sameAddr compara como antes la clave addr.String() (IP + puerto + zona), sin asignar strings.
func sameAddr(a, b *net.UDPAddr) bool {
	return a.Port == b.Port && a.IP.Equal(b.IP) && a.Zone == b.Zone
}

func indexOfPeer(peers []*net.UDPAddr, addr *net.UDPAddr) int {
	for index, peer := range peers {
		if sameAddr(peer, addr) {
			return index
		}
	}
	return -1
}

// snapshot devuelve la lista publicada. Es de solo lectura.
func (u *UDP) snapshot() []*net.UDPAddr {
	if peers := u.peers.Load(); peers != nil {
		return *peers
	}
	return nil
}

// AddPeer se llama por cada paquete que recibe el host: el caso normal (peer ya conocido) se
// resuelve con una carga atómica y una búsqueda lineal, sin lock.
func (u *UDP) AddPeer(addr *net.UDPAddr) {
	if addr == nil || indexOfPeer(u.snapshot(), addr) >= 0 {
		return
	}
	u.peersMu.Lock()
	defer u.peersMu.Unlock()
	current := u.snapshot()
	if indexOfPeer(current, addr) >= 0 {
		return
	}
	next := make([]*net.UDPAddr, len(current), len(current)+1)
	copy(next, current)
	next = append(next, addr)
	u.peers.Store(&next)
}

func (u *UDP) RemovePeer(addr *net.UDPAddr) {
	if addr == nil {
		return
	}
	u.peersMu.Lock()
	defer u.peersMu.Unlock()
	current := u.snapshot()
	index := indexOfPeer(current, addr)
	if index < 0 {
		return
	}
	next := make([]*net.UDPAddr, 0, len(current)-1)
	next = append(next, current[:index]...)
	next = append(next, current[index+1:]...)
	u.peers.Store(&next)
}

// Peers devuelve una copia de la lista: el llamador puede modificarla.
func (u *UDP) Peers() []*net.UDPAddr {
	return append([]*net.UDPAddr(nil), u.snapshot()...)
}

func (u *UDP) Send(addr *net.UDPAddr, packet Packet) error {
	buf := sealBuffers.Get().(*[]byte)
	data := u.cipher.seal(*buf, packet)
	_, err := u.conn.WriteToUDP(data, addr)
	*buf = data[:0]
	sealBuffers.Put(buf)
	return err
}

func (u *UDP) Broadcast(packet Packet, except *net.UDPAddr) {
	peers := u.snapshot()
	if len(peers) == 0 {
		return
	}
	buf := sealBuffers.Get().(*[]byte)
	data := u.cipher.seal(*buf, packet) // una sola vez para todos los peers
	u.writeAll(peers, data, except)
	*buf = data[:0]
	sealBuffers.Put(buf)
}

// Relay reenvía un paquete recibido (Receive) a todos los peers menos except, con los mismos
// bytes que llegaron: ya se verificó al abrirlo y todos comparten la clave.
func (u *UDP) Relay(packet Packet, except *net.UDPAddr) {
	if packet.sealed == nil {
		u.Broadcast(packet, except)
		return
	}
	u.writeAll(u.snapshot(), packet.sealed, except)
}

func (u *UDP) writeAll(peers []*net.UDPAddr, data []byte, except *net.UDPAddr) {
	for _, peer := range peers {
		if except != nil && sameAddr(peer, except) {
			continue
		}
		_, _ = u.conn.WriteToUDP(data, peer)
	}
}

// Receive lee un paquete. No es seguro para lectores concurrentes (reutiliza readBuf); solo lo
// llama receiveLoop. El paquete devuelto es una copia del tamaño justo: los handlers asíncronos
// (chat, assets) retienen el payload después de la próxima lectura. Una sola asignación por
// paquete: los bytes sellados (para Relay) y el payload descifrado van en el mismo bloque.
// Lo que no abre con la clave de la sala (scanners de Internet, una invitación vieja o
// equivocada, un paquete alterado) se descarta acá en silencio y se cuenta en Invalid. Solo
// devuelve errores del socket.
func (u *UDP) Receive() (Packet, *net.UDPAddr, error) {
	for {
		n, addr, err := u.conn.ReadFromUDP(u.readBuf)
		if err != nil {
			return Packet{}, nil, err
		}
		if !validFrame(u.readBuf[:n]) {
			u.reject(addr)
			continue
		}
		block := make([]byte, n+n-sealedSize(0))
		sealed := block[:n:n]
		copy(sealed, u.readBuf[:n])
		packet, err := u.cipher.open(block[n:], sealed)
		if err != nil {
			u.reject(addr)
			continue
		}
		packet.sealed = sealed
		return packet, addr, nil
	}
}

func (u *UDP) reject(addr *net.UDPAddr) {
	u.invalid.Add(1)
	u.lastInvalid.Store(addr)
}

// Invalid cuenta los datagramas descartados porque no abren con la clave de la sala.
func (u *UDP) Invalid() uint64 { return u.invalid.Load() }

// LastInvalid es el origen del último datagrama descartado (nil si no hubo).
func (u *UDP) LastInvalid() *net.UDPAddr { return u.lastInvalid.Load() }

// SendBulk encola un paquete de archivo para addr (ritmo BulkRateBytes). Espera si la cola está
// llena, así que se llama desde goroutines propias, nunca desde receiveLoop. Devuelve false si el
// socket se cerró.
func (u *UDP) SendBulk(addr *net.UDPAddr, packet Packet) bool {
	if addr == nil {
		return false
	}
	return u.enqueue(u.bulk, laneJob{packet: packet, to: addr}, true, nil)
}

// BroadcastBulk es SendBulk a todos los peers menos except.
func (u *UDP) BroadcastBulk(packet Packet, except *net.UDPAddr) bool {
	return u.enqueue(u.bulk, laneJob{packet: packet, except: except, broadcast: true}, true, nil)
}

// TryRelayBulk encola sin esperar el reenvío de un paquete recibido (host, desde receiveLoop),
// con los mismos bytes que llegaron. Con la cola llena el paquete no se reenvía: a quien le falte
// lo pide de nuevo (AssetMissing).
func (u *UDP) TryRelayBulk(packet Packet, except *net.UDPAddr) bool {
	return u.enqueue(u.bulk, laneJob{packet: packet, sealed: packet.sealed, except: except, broadcast: true}, false, nil)
}

// SendScreen encola un paquete de pantalla para targets (cola de pantalla, ScreenRateBytes o lo
// que diga SetScreenRate). Espera si la cola está llena: lo llama la goroutine de quien comparte,
// nunca receiveLoop. Devuelve false si el socket o stop se cerraron. targets no se modifica después.
func (u *UDP) SendScreen(targets []*net.UDPAddr, packet Packet, stop <-chan struct{}) bool {
	if len(targets) == 0 {
		return false
	}
	return u.enqueue(u.screen, laneJob{packet: packet, targets: targets}, true, stop)
}

// TryRelayScreen encola sin esperar el reenvío de un tile recibido (host, desde receiveLoop) a
// targets, con los mismos bytes que llegaron. Con la cola llena se descarta: ese tile queda viejo
// hasta que vuelva a cambiar o hasta la próxima imagen completa. targets no se modifica después.
func (u *UDP) TryRelayScreen(packet Packet, targets []*net.UDPAddr) bool {
	if len(targets) == 0 {
		return false
	}
	return u.enqueue(u.screen, laneJob{packet: packet, sealed: packet.sealed, targets: targets}, false, nil)
}

// SetScreenRate cambia el ritmo de la cola de pantalla (bytes por segundo sumando destinos), entre
// screenMinRate y ScreenLANRateBytes. El host elige LAN o internet; quien comparte desde un
// cliente usa lo que le dice el host (PacketScreenRate), así lo que reenvía el host entra en su tope.
func (u *UDP) SetScreenRate(bytesPerSecond int) {
	u.screen.rate.Store(int64(min(max(bytesPerSecond, screenMinRate), ScreenLANRateBytes)))
}

// ScreenRate es el ritmo actual de la cola de pantalla.
func (u *UDP) ScreenRate() int { return int(u.screen.rate.Load()) }

// SendTo sella el paquete una vez y lo manda ya a cada destino, sin cola (el cursor de la pantalla
// compartida: chico y urgente).
func (u *UDP) SendTo(targets []*net.UDPAddr, packet Packet) {
	if len(targets) == 0 {
		return
	}
	buf := sealBuffers.Get().(*[]byte)
	data := u.cipher.seal(*buf, packet)
	u.writeAll(targets, data, nil)
	*buf = data[:0]
	sealBuffers.Put(buf)
}

// RelayTo reenvía un paquete recibido a targets con los mismos bytes, sin cola.
func (u *UDP) RelayTo(packet Packet, targets []*net.UDPAddr) {
	if packet.sealed == nil {
		u.SendTo(targets, packet)
		return
	}
	u.writeAll(targets, packet.sealed, nil)
}

// ScreenCounters devuelve los bytes que salieron por la cola de pantalla (sumando destinos) y los
// reenvíos descartados por cola llena, desde que se abrió el socket.
func (u *UDP) ScreenCounters() (sentBytes, dropped uint64) {
	return u.screen.sentBytes.Load(), u.screen.dropped.Load()
}

func (u *UDP) enqueue(l *lane, job laneJob, wait bool, stop <-chan struct{}) bool {
	l.once.Do(func() { go u.laneLoop(l) })
	if !wait {
		select {
		case l.jobs <- job:
			return true
		default:
			l.dropped.Add(1)
			return false
		}
	}
	select {
	case l.jobs <- job:
		return true
	case <-u.closed:
		return false
	case <-stop:
		return false
	}
}

// laneLoop manda lo encolado en l al ritmo de su token bucket.
func (u *UDP) laneLoop(l *lane) {
	bucket := newTokenBucket(&l.rate, l.burst)
	defer bucket.stop()
	var buf []byte // esta goroutine es la única que sella lo de esta cola: se reutiliza
	for {
		var job laneJob
		select {
		case <-u.closed:
			return
		case job = <-l.jobs:
		}
		data := job.sealed
		if data == nil {
			buf = u.cipher.seal(buf, job.packet)
			data = buf
		}
		targets := job.targets
		switch {
		case job.broadcast:
			targets = u.snapshot()
		case targets == nil:
			targets = []*net.UDPAddr{job.to}
		}
		for _, peer := range targets {
			if job.except != nil && sameAddr(peer, job.except) {
				continue
			}
			if !bucket.wait(len(data), u.closed) {
				return
			}
			if _, err := u.conn.WriteToUDP(data, peer); err == nil {
				l.sentBytes.Add(uint64(len(data)))
			}
		}
	}
}

// tokenBucket reparte crédito en bytes a ritmo constante (rate, que puede cambiar en vivo), con un
// tope para las ráfagas: minBurst o burstWindow de datos a la tasa actual, lo que sea más grande
// (a 20 Mbps una ráfaga de 6 KB era un timer por paquete).
type tokenBucket struct {
	rate             *atomic.Int64
	minBurst, tokens float64
	last             time.Time
	timer            *time.Timer
}

// burstWindow es cuánto tiempo de datos entra en una ráfaga a la tasa actual.
const burstWindow = 5 * time.Millisecond

func newTokenBucket(rate *atomic.Int64, burst int) *tokenBucket {
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	return &tokenBucket{rate: rate, minBurst: float64(burst), tokens: float64(burst), last: time.Now(), timer: timer}
}

// wait espera hasta tener n bytes de crédito y los gasta (un paquete más grande que la ráfaga
// espera la ráfaga entera). Devuelve false si stop se cerró.
func (b *tokenBucket) wait(n int, stop <-chan struct{}) bool {
	for {
		rate := float64(max(b.rate.Load(), 1))
		burst := max(b.minBurst, rate*burstWindow.Seconds())
		need := min(float64(n), burst)
		now := time.Now()
		b.tokens = min(burst, b.tokens+rate*now.Sub(b.last).Seconds())
		b.last = now
		if b.tokens >= need {
			b.tokens -= need
			return true
		}
		b.timer.Reset(time.Duration((need - b.tokens) / rate * float64(time.Second)))
		select {
		case <-stop:
			return false
		case <-b.timer.C:
		}
	}
}

func (b *tokenBucket) stop() { b.timer.Stop() }

func (u *UDP) Close() error {
	u.closeOnce.Do(func() { close(u.closed) })
	return u.conn.Close()
}
