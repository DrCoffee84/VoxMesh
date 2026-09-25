package transport

import (
	"encoding/binary"
	"fmt"
	"net"
	"sync"
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
)

type Packet struct {
	Kind     byte
	Sequence uint32
	Payload  []byte
}

func encode(packet Packet) []byte {
	data := make([]byte, HeaderSize+len(packet.Payload))
	copy(data[:4], magic)
	data[4] = packet.Kind
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
	return Packet{Kind: data[4], Sequence: binary.BigEndian.Uint32(data[8:12]), Payload: data[HeaderSize:]}, nil
}

type UDP struct {
	conn  *net.UDPConn
	mu    sync.RWMutex
	peers map[string]*net.UDPAddr
}

func Listen(address string) (*UDP, error) {
	addr, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return nil, err
	}
	return &UDP{conn: conn, peers: make(map[string]*net.UDPAddr)}, nil
}

func (u *UDP) Address() *net.UDPAddr { return u.conn.LocalAddr().(*net.UDPAddr) }

func (u *UDP) AddPeer(addr *net.UDPAddr) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.peers[addr.String()] = addr
}

func (u *UDP) RemovePeer(addr *net.UDPAddr) {
	if addr == nil {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	delete(u.peers, addr.String())
}

func (u *UDP) Peers() []*net.UDPAddr {
	u.mu.RLock()
	defer u.mu.RUnlock()
	peers := make([]*net.UDPAddr, 0, len(u.peers))
	for _, peer := range u.peers {
		peers = append(peers, peer)
	}
	return peers
}

func (u *UDP) Send(addr *net.UDPAddr, packet Packet) error {
	_, err := u.conn.WriteToUDP(encode(packet), addr)
	return err
}

func (u *UDP) Broadcast(packet Packet, except *net.UDPAddr) {
	for _, peer := range u.Peers() {
		if except != nil && peer.String() == except.String() {
			continue
		}
		_, _ = u.conn.WriteToUDP(encode(packet), peer)
	}
}

func (u *UDP) Receive() (Packet, *net.UDPAddr, error) {
	buffer := make([]byte, 64*1024)
	n, addr, err := u.conn.ReadFromUDP(buffer)
	if err != nil {
		return Packet{}, nil, err
	}
	packet, err := decode(buffer[:n])
	return packet, addr, err
}

func (u *UDP) Close() error { return u.conn.Close() }
