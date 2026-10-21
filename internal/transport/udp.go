package transport

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"syscall"
)

const (
	magic              = "VM01"
	HeaderSize         = 16
	PacketAudio        = 1
	PacketHello        = 2
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
)

func PacketKindName(kind byte) string {
	switch kind {
	case PacketAudio:
		return "Audio"
	case PacketHello:
		return "Hello"
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
	default:
		return fmt.Sprintf("Unknown(%d)", kind)
	}
}

// FlagAudioSequence indica que Sequence es la secuencia de audio propia del emisor:
// contigua, solo avanza con paquetes de audio. Viaja en el byte 5 del header; sin el flag,
// Sequence es el contador global.
const FlagAudioSequence byte = 1 << 0

// SocketBufferBytes es el tamaño pedido para los buffers de envío y recepción del socket:
// absorbe ráfagas (reenvío del host, sincronización de assets) sin que el SO descarte audio.
const SocketBufferBytes = 4 << 20

type Packet struct {
	Kind     byte
	Flags    byte
	Sequence uint32
	// Talkspurt (byte 6) es el número de frase del audio: el emisor lo incrementa cada vez que
	// vuelve a mandar después de un silencio (compuerta cerrada, mute). El receptor lo usa para
	// no medir el silencio como retardo de red (secuencia contigua, llegada muy posterior).
	Talkspurt byte
	Payload   []byte
}

func encode(packet Packet) []byte {
	data := make([]byte, HeaderSize+len(packet.Payload))
	copy(data[:4], magic)
	data[4] = packet.Kind
	data[5] = packet.Flags
	data[6] = packet.Talkspurt
	binary.BigEndian.PutUint32(data[8:12], packet.Sequence)
	binary.BigEndian.PutUint32(data[12:16], uint32(len(packet.Payload)))
	copy(data[HeaderSize:], packet.Payload)
	return data
}

const MaxPayloadSize = 65507 - HeaderSize

func decode(data []byte) (Packet, error) {
	if len(data) < HeaderSize || len(data) > 65507 || string(data[:4]) != magic {
		return Packet{}, fmt.Errorf("invalid VoxMesh packet")
	}
	payloadLength := int(binary.BigEndian.Uint32(data[12:16]))
	if payloadLength < 0 || payloadLength > MaxPayloadSize || payloadLength != len(data)-HeaderSize {
		return Packet{}, fmt.Errorf("invalid payload length")
	}
	return Packet{Kind: data[4], Flags: data[5], Talkspurt: data[6], Sequence: binary.BigEndian.Uint32(data[8:12]), Payload: data[HeaderSize:]}, nil
}

// UDP guarda los peers como una lista copy-on-write: Broadcast (reenvío del host, audio
// saliente) y el AddPeer de cada paquete recibido leen la instantánea con una carga atómica,
// sin lock ni asignaciones. Solo los cambios reales de la lista toman peersMu, que serializa a
// los escritores entre sí y nunca a un lector.
type UDP struct {
	conn    *net.UDPConn
	peersMu sync.Mutex
	peers   atomic.Pointer[[]*net.UDPAddr] // inmutable una vez publicada
	readBuf []byte                         // buffer de lectura de Receive (un solo lector: receiveLoop)
}

func Listen(address string) (*UDP, error) {
	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			var controlErr error
			err := c.Control(func(fd uintptr) {
				controlErr = setReusePort(fd)
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
	return &UDP{conn: conn, readBuf: make([]byte, 64*1024)}, nil
}

func (u *UDP) Address() *net.UDPAddr { return u.conn.LocalAddr().(*net.UDPAddr) }

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
	_, err := u.conn.WriteToUDP(encode(packet), addr)
	return err
}

func (u *UDP) Broadcast(packet Packet, except *net.UDPAddr) {
	peers := u.snapshot()
	if len(peers) == 0 {
		return
	}
	data := encode(packet) // una sola vez: antes se codificaba (y asignaba) por cada peer
	for _, peer := range peers {
		if except != nil && sameAddr(peer, except) {
			continue
		}
		_, _ = u.conn.WriteToUDP(data, peer)
	}
}

// Receive lee un paquete. No es seguro para lectores concurrentes (reutiliza readBuf); solo lo
// llama receiveLoop. El paquete devuelto es una copia del tamaño justo: los handlers asíncronos
// (chat, assets) retienen el payload después de la próxima lectura.
func (u *UDP) Receive() (Packet, *net.UDPAddr, error) {
	n, addr, err := u.conn.ReadFromUDP(u.readBuf)
	if err != nil {
		return Packet{}, nil, err
	}
	packet, err := decode(append([]byte(nil), u.readBuf[:n]...))
	return packet, addr, err
}

func (u *UDP) Close() error { return u.conn.Close() }
