package screen

import (
	"encoding/binary"
	"errors"
	"time"
)

// Reparación en vez de mandar la imagen completa cada tanto (que costaba 300–570 kbps con la
// pantalla quieta). Quien comparte manda cada DigestInterval un resumen con el serial (16 bits
// bajos) del último envío de cada tile, detrás de los tiles de esa vuelta y por la misma cola;
// quien mira compara con lo que tiene (Assembler.Stale) y pide solo los que le faltan.
//
// Digest (payload de transport.PacketScreenDigest):
//
//	[0:8] ID de quien comparte  [8:12] sesión  [12:14] ancho  [14:16] alto
//	[16:18] primer tile  [18:20] cantidad  [20:] serial u16 por tile (0 = nunca se mandó)
//
// Repair (payload de transport.PacketScreenRepair, de quien mira a quien comparte por el host):
//
//	[0:8] ID de quien comparte  [8:12] sesión  [12:14] cantidad  [14:] tile u16 por cada uno
const (
	DigestInterval   = 2 * time.Second
	DigestMaxTiles   = 600 // un resumen o un pedido entra en un datagrama de ~1,2 KB
	digestHeaderSize = 20
	repairHeaderSize = 14
)

// Digest es un pedazo del resumen: los seriales de los tiles First..First+len(Serials).
type Digest struct {
	Sharer        PeerID
	Session       uint32
	Width, Height int
	First         int
	Serials       []uint16
}

// Repair son los tiles que quien mira tiene viejos.
type Repair struct {
	Sharer  PeerID
	Session uint32
	Tiles   []int
}

var (
	errBadDigest = errors.New("resumen de pantalla inválido")
	errBadRepair = errors.New("pedido de reparación inválido")
)

func AppendDigest(dst []byte, d Digest) []byte {
	var header [digestHeaderSize]byte
	copy(header[0:8], d.Sharer[:])
	binary.BigEndian.PutUint32(header[8:12], d.Session)
	binary.BigEndian.PutUint16(header[12:14], uint16(d.Width))
	binary.BigEndian.PutUint16(header[14:16], uint16(d.Height))
	binary.BigEndian.PutUint16(header[16:18], uint16(d.First))
	binary.BigEndian.PutUint16(header[18:20], uint16(len(d.Serials)))
	dst = append(dst, header[:]...)
	for _, serial := range d.Serials {
		dst = binary.BigEndian.AppendUint16(dst, serial)
	}
	return dst
}

func DecodeDigest(payload []byte) (Digest, error) {
	if len(payload) < digestHeaderSize {
		return Digest{}, errBadDigest
	}
	var d Digest
	copy(d.Sharer[:], payload[0:8])
	d.Session = binary.BigEndian.Uint32(payload[8:12])
	d.Width = int(binary.BigEndian.Uint16(payload[12:14]))
	d.Height = int(binary.BigEndian.Uint16(payload[14:16]))
	d.First = int(binary.BigEndian.Uint16(payload[16:18]))
	count := int(binary.BigEndian.Uint16(payload[18:20]))
	if d.Width <= 0 || d.Height <= 0 || d.Width > MaxWidth || d.Height > MaxHeight || count == 0 || count > DigestMaxTiles ||
		len(payload) != digestHeaderSize+2*count || d.First+count > NewGrid(d.Width, d.Height).Count() {
		return Digest{}, errBadDigest
	}
	d.Serials = make([]uint16, count)
	for i := range d.Serials {
		d.Serials[i] = binary.BigEndian.Uint16(payload[digestHeaderSize+2*i:])
	}
	return d, nil
}

func AppendRepair(dst []byte, r Repair) []byte {
	var header [repairHeaderSize]byte
	copy(header[0:8], r.Sharer[:])
	binary.BigEndian.PutUint32(header[8:12], r.Session)
	binary.BigEndian.PutUint16(header[12:14], uint16(len(r.Tiles)))
	dst = append(dst, header[:]...)
	for _, tile := range r.Tiles {
		dst = binary.BigEndian.AppendUint16(dst, uint16(tile))
	}
	return dst
}

func DecodeRepair(payload []byte) (Repair, error) {
	if len(payload) < repairHeaderSize {
		return Repair{}, errBadRepair
	}
	var r Repair
	copy(r.Sharer[:], payload[0:8])
	r.Session = binary.BigEndian.Uint32(payload[8:12])
	count := int(binary.BigEndian.Uint16(payload[12:14]))
	if count == 0 || count > DigestMaxTiles || len(payload) != repairHeaderSize+2*count {
		return Repair{}, errBadRepair
	}
	r.Tiles = make([]int, count)
	for i := range r.Tiles {
		r.Tiles[i] = int(binary.BigEndian.Uint16(payload[repairHeaderSize+2*i:]))
	}
	return r, nil
}

// Stale devuelve los tiles del resumen que este Assembler tiene viejos: los que se mandaron
// (serial ≠ 0), no son el último que se aplicó y no están a medio llegar con ese serial. Un resumen
// de otra sesión o de otro tamaño no pide nada.
func (a *Assembler) Stale(d Digest) []int {
	if d.Session != a.session || d.Width != a.grid.W || d.Height != a.grid.H || d.First+len(d.Serials) > len(a.applied) {
		return nil
	}
	var stale []int
	for i, want := range d.Serials {
		tile := d.First + i
		if want == 0 || uint16(a.applied[tile]) == want {
			continue
		}
		if entry := a.pending[tile]; entry != nil && uint16(entry.serial) == want {
			continue // llegando
		}
		stale = append(stale, tile)
	}
	return stale
}
