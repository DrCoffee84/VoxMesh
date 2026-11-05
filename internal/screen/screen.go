// Package screen es la pantalla compartida sin la interfaz: captura (GDI, solo Windows), tiles que
// cambiaron, PNG sin pérdida (el texto tiene que leerse nítido), chunks del tamaño de los de
// internal/assets, rearmado del lado del que mira y el filtro de área para achicar. No usa cgo:
// se prueba sin pantalla salvo capture_windows_test.go.
package screen

import (
	"encoding/hex"
	"time"

	"voxmesh/internal/assets"
)

const (
	// TileSize es el lado de un tile. 64 px: el cursor o una línea que se escribe cambian pocos
	// tiles chicos en vez de uno grande.
	TileSize = 64
	// DefaultMaxWidth es el ancho máximo si no se elige "Resolución completa": más ancho se achica
	// con el filtro de área (Scaler).
	DefaultMaxWidth = 1920
	// MaxWidth y MaxHeight acotan lo que acepta quien mira (8K).
	MaxWidth  = 7680
	MaxHeight = 4320

	// ChunkDataSize es el tamaño de los chunks de internal/assets: no fragmenta en internet.
	ChunkDataSize = assets.ChunkDataSize
	// MaxTileChunks: el PNG de un tile de 64×64 RGB en el peor caso (ruido, bloques sin comprimir)
	// ocupa ~12,4 KB, o sea 11 chunks.
	MaxTileChunks = 12
	// MaxPending es el tope de tiles a medio llegar del lado del que mira.
	MaxPending = 1024
	// PendingTTL: un tile incompleto se descarta a los 2 s (se perdió un chunk).
	PendingTTL = 2 * time.Second
)

// PeerID es el ID de participante en crudo (8 bytes, 16 caracteres hex), como en el audio.
type PeerID [8]byte

// ParsePeerID convierte el ID hex de room.Participant.
func ParsePeerID(id string) (PeerID, bool) {
	var peer PeerID
	if hex.DecodedLen(len(id)) != len(peer) {
		return peer, false
	}
	if _, err := hex.Decode(peer[:], []byte(id)); err != nil {
		return peer, false
	}
	return peer, true
}

func (p PeerID) String() string { return hex.EncodeToString(p[:]) }
