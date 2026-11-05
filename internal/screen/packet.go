package screen

import (
	"encoding/binary"
	"errors"
)

// HeaderSize es la cabecera de cada chunk de tile (payload de transport.PacketScreenTile):
//
//	[0:8]   ID de quien comparte (PeerID)
//	[8:12]  sesión (u32): cambia al empezar a compartir y cuando cambia el tamaño del cuadro
//	[12:14] ancho del cuadro  [14:16] alto
//	[16:18] tile (orden de filas de Grid; CursorTile = la forma del cursor)
//	[18:22] serial (u32): crece con cada tile que sale en la sesión; por tile gana el más alto
//	        (en CursorTile, el número de la forma)
//	[22]    índice del chunk  [23] total de chunks del tile
//	[24:]   pedazo del PNG del tile (≤ ChunkDataSize)
const HeaderSize = 24

// TileChunk es un pedazo del PNG de un tile.
type TileChunk struct {
	Sharer        PeerID
	Session       uint32
	Width, Height int
	Tile          int
	Serial        uint32
	Index, Total  int
	Data          []byte
}

var errBadChunk = errors.New("chunk de pantalla inválido")

// ChunkCount es cuántos chunks ocupa un PNG de n bytes.
func ChunkCount(n int) int { return max(1, (n+ChunkDataSize-1)/ChunkDataSize) }

// AppendChunk agrega a dst el payload del chunk index de png (los campos de c salvo Index, Total
// y Data) y lo devuelve.
func AppendChunk(dst []byte, c TileChunk, png []byte, index int) []byte {
	total := ChunkCount(len(png))
	start := index * ChunkDataSize
	end := min(start+ChunkDataSize, len(png))
	var header [HeaderSize]byte
	copy(header[0:8], c.Sharer[:])
	binary.BigEndian.PutUint32(header[8:12], c.Session)
	binary.BigEndian.PutUint16(header[12:14], uint16(c.Width))
	binary.BigEndian.PutUint16(header[14:16], uint16(c.Height))
	binary.BigEndian.PutUint16(header[16:18], uint16(c.Tile))
	binary.BigEndian.PutUint32(header[18:22], c.Serial)
	header[22] = byte(index)
	header[23] = byte(total)
	dst = append(dst, header[:]...)
	return append(dst, png[start:end]...)
}

// DecodeChunk lee un payload de PacketScreenTile y lo valida contra los topes: lo que viene de la
// red no reserva más que eso. Data apunta dentro de payload.
func DecodeChunk(payload []byte) (TileChunk, error) {
	if len(payload) <= HeaderSize || len(payload) > HeaderSize+ChunkDataSize {
		return TileChunk{}, errBadChunk
	}
	var c TileChunk
	copy(c.Sharer[:], payload[0:8])
	c.Session = binary.BigEndian.Uint32(payload[8:12])
	c.Width = int(binary.BigEndian.Uint16(payload[12:14]))
	c.Height = int(binary.BigEndian.Uint16(payload[14:16]))
	c.Tile = int(binary.BigEndian.Uint16(payload[16:18]))
	c.Serial = binary.BigEndian.Uint32(payload[18:22])
	c.Index, c.Total = int(payload[22]), int(payload[23])
	c.Data = payload[HeaderSize:]
	if c.Width <= 0 || c.Height <= 0 || c.Width > MaxWidth || c.Height > MaxHeight ||
		c.Tile >= NewGrid(c.Width, c.Height).Count() && c.Tile != CursorTile || c.Serial == 0 ||
		c.Total <= 0 || c.Total > MaxTileChunks || c.Index >= c.Total {
		return TileChunk{}, errBadChunk
	}
	return c, nil
}
