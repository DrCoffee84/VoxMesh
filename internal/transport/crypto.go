package transport

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"sync/atomic"
)

// Key es la clave de la sala: 32 bytes aleatorios por cada "Iniciar sala", solo en memoria. Viaja
// en la invitación y la tienen todos los de la sala, así que sobrevive a la migración de host.
type Key [32]byte

// NewKey genera una clave de sala nueva.
func NewKey() (Key, error) {
	var key Key
	_, err := rand.Read(key[:])
	return key, err
}

// Cada datagrama lleva, después de la cabecera, el nonce y el tag de AES-256-GCM.
const (
	nonceSize    = 12
	tagSize      = 16
	SealOverhead = nonceSize + tagSize
)

// maxDatagram es el máximo de un datagrama UDP sobre IPv4.
const maxDatagram = 65507

var errInvalidPacket = errors.New("invalid VoxMesh packet")

// roomCipher sella y abre los paquetes de la sala. Nonce = prefijo aleatorio de 4 bytes (uno por
// socket) + contador de 64 bits: con la misma clave nunca se repite. Es seguro para varias
// goroutines (el contador es atómico; AEAD no guarda estado).
type roomCipher struct {
	aead    cipher.AEAD
	prefix  [4]byte
	counter atomic.Uint64
}

func newRoomCipher(key Key) (*roomCipher, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	c := &roomCipher{aead: aead}
	if _, err := rand.Read(c.prefix[:]); err != nil {
		return nil, err
	}
	return c, nil
}

// sealedSize es el tamaño del datagrama sellado de un payload de n bytes.
func sealedSize(n int) int { return HeaderSize + SealOverhead + n }

// seal arma el datagrama de packet sobre dst (reutiliza su capacidad: sin asignaciones si
// alcanza) y lo devuelve. La cabecera va en claro y autenticada (AAD).
func (c *roomCipher) seal(dst []byte, packet Packet) []byte {
	size := sealedSize(len(packet.Payload))
	if cap(dst) < size {
		dst = make([]byte, 0, size)
	}
	dst = dst[:HeaderSize+nonceSize]
	copy(dst[:4], magic)
	dst[4] = packet.Kind
	dst[5] = packet.Flags
	dst[6] = packet.Talkspurt
	dst[7] = 0
	binary.BigEndian.PutUint32(dst[8:12], packet.Sequence)
	binary.BigEndian.PutUint32(dst[12:16], uint32(SealOverhead+len(packet.Payload)))
	copy(dst[16:20], c.prefix[:])
	binary.BigEndian.PutUint64(dst[20:28], c.counter.Add(1))
	return c.aead.Seal(dst, dst[HeaderSize:HeaderSize+nonceSize], packet.Payload, dst[:HeaderSize])
}

// validFrame dice si data tiene la forma de un datagrama VoxMesh (magic y largos), sin abrirlo.
func validFrame(data []byte) bool {
	if len(data) < sealedSize(0) || len(data) > maxDatagram || string(data[:4]) != magic {
		return false
	}
	return int(binary.BigEndian.Uint32(data[12:16])) == len(data)-HeaderSize
}

// open verifica y descifra data. El payload se escribe en dst (reutiliza su capacidad: sin
// asignaciones si alcanza para len(data)-sealedSize(0)). Un paquete alterado o de otra clave da
// error.
func (c *roomCipher) open(dst, data []byte) (Packet, error) {
	if !validFrame(data) {
		return Packet{}, errInvalidPacket
	}
	header := data[:HeaderSize]
	payload, err := c.aead.Open(dst[:0], data[HeaderSize:HeaderSize+nonceSize], data[HeaderSize+nonceSize:], header)
	if err != nil {
		return Packet{}, err
	}
	return Packet{
		Kind:      header[4],
		Flags:     header[5],
		Talkspurt: header[6],
		Sequence:  binary.BigEndian.Uint32(header[8:12]),
		Payload:   payload,
	}, nil
}
