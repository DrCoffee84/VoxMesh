// Package assets splits large binary payloads (images, and future soundboard clips)
// into small UDP-sized chunks and reassembles them on the receiving side, with a
// best-effort retransmission mechanism for chunks lost in transit.
package assets

import (
	"encoding/binary"
	"fmt"
	"sync"
	"time"
)

const (
	KindImage = 1
	KindSound = 2

	// ChunkDataSize keeps encoded packets safely under typical Wi-Fi MTUs.
	ChunkDataSize = 1200
	// MaxBytes bounds both outgoing files and incoming reassembly buffers.
	MaxBytes = 8 * 1024 * 1024
)

type Chunk struct {
	Kind  byte
	ID    string
	Ext   string
	Index uint32
	Total uint32
	Data  []byte
}

// Split breaks data into ordered chunks ready to be sent as individual packets.
func Split(kind byte, id, ext string, data []byte) ([]Chunk, error) {
	if len(data) > MaxBytes {
		return nil, fmt.Errorf("el archivo supera el límite de %d MiB", MaxBytes/1024/1024)
	}
	total := uint32((len(data) + ChunkDataSize - 1) / ChunkDataSize)
	if total == 0 {
		total = 1
	}
	chunks := make([]Chunk, 0, total)
	for index := uint32(0); index < total; index++ {
		start := int(index) * ChunkDataSize
		end := start + ChunkDataSize
		if end > len(data) {
			end = len(data)
		}
		chunks = append(chunks, Chunk{Kind: kind, ID: id, Ext: ext, Index: index, Total: total, Data: data[start:end]})
	}
	return chunks, nil
}

func Encode(chunk Chunk) []byte {
	buffer := make([]byte, 0, 10+len(chunk.ID)+len(chunk.Ext)+len(chunk.Data))
	buffer = append(buffer, chunk.Kind, byte(len(chunk.ID)))
	buffer = append(buffer, chunk.ID...)
	buffer = append(buffer, byte(len(chunk.Ext)))
	buffer = append(buffer, chunk.Ext...)
	var indexAndTotal [8]byte
	binary.BigEndian.PutUint32(indexAndTotal[0:4], chunk.Index)
	binary.BigEndian.PutUint32(indexAndTotal[4:8], chunk.Total)
	buffer = append(buffer, indexAndTotal[:]...)
	buffer = append(buffer, chunk.Data...)
	return buffer
}

func Decode(payload []byte) (Chunk, error) {
	if len(payload) < 2 {
		return Chunk{}, fmt.Errorf("chunk demasiado corto")
	}
	kind, idLen, offset := payload[0], int(payload[1]), 2
	if offset+idLen+1 > len(payload) {
		return Chunk{}, fmt.Errorf("chunk inválido")
	}
	id := string(payload[offset : offset+idLen])
	offset += idLen
	extLen := int(payload[offset])
	offset++
	if offset+extLen+8 > len(payload) {
		return Chunk{}, fmt.Errorf("chunk inválido")
	}
	ext := string(payload[offset : offset+extLen])
	offset += extLen
	index := binary.BigEndian.Uint32(payload[offset : offset+4])
	total := binary.BigEndian.Uint32(payload[offset+4 : offset+8])
	offset += 8
	return Chunk{Kind: kind, ID: id, Ext: ext, Index: index, Total: total, Data: payload[offset:]}, nil
}

// MissingRequest asks the origin to (re)send specific chunk indices, or all of
// them when Missing is empty (used when a late joiner has no chunks at all yet).
type MissingRequest struct {
	Kind    byte
	ID      string
	Ext     string
	Missing []uint32
}

func EncodeMissing(request MissingRequest) []byte {
	buffer := make([]byte, 0, 6+len(request.ID)+len(request.Ext)+4*len(request.Missing))
	buffer = append(buffer, request.Kind, byte(len(request.ID)))
	buffer = append(buffer, request.ID...)
	buffer = append(buffer, byte(len(request.Ext)))
	buffer = append(buffer, request.Ext...)
	var count [4]byte
	binary.BigEndian.PutUint32(count[:], uint32(len(request.Missing)))
	buffer = append(buffer, count[:]...)
	for _, index := range request.Missing {
		var indexBytes [4]byte
		binary.BigEndian.PutUint32(indexBytes[:], index)
		buffer = append(buffer, indexBytes[:]...)
	}
	return buffer
}

func DecodeMissing(payload []byte) (MissingRequest, error) {
	if len(payload) < 2 {
		return MissingRequest{}, fmt.Errorf("solicitud inválida")
	}
	kind, idLen, offset := payload[0], int(payload[1]), 2
	if offset+idLen+1 > len(payload) {
		return MissingRequest{}, fmt.Errorf("solicitud inválida")
	}
	id := string(payload[offset : offset+idLen])
	offset += idLen
	extLen := int(payload[offset])
	offset++
	if offset+extLen+4 > len(payload) {
		return MissingRequest{}, fmt.Errorf("solicitud inválida")
	}
	ext := string(payload[offset : offset+extLen])
	offset += extLen
	count := binary.BigEndian.Uint32(payload[offset : offset+4])
	offset += 4
	missing := make([]uint32, 0, count)
	for i := uint32(0); i < count && offset+4 <= len(payload); i++ {
		missing = append(missing, binary.BigEndian.Uint32(payload[offset:offset+4]))
		offset += 4
	}
	return MissingRequest{Kind: kind, ID: id, Ext: ext, Missing: missing}, nil
}

type pendingAsset struct {
	ext       string
	total     uint32
	chunks    map[uint32][]byte
	origin    string
	lastChunk time.Time
	attempts  int
}

// Assembler reassembles chunks arriving out of order or split across retries.
type Assembler struct {
	mu      sync.Mutex
	pending map[string]*pendingAsset
}

func NewAssembler() *Assembler {
	return &Assembler{pending: make(map[string]*pendingAsset)}
}

// Ingest records a chunk and returns the reassembled bytes once complete.
// origin is the address the chunk arrived from, used to target retry requests.
func (a *Assembler) Ingest(chunk Chunk, origin string) ([]byte, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	entry, exists := a.pending[chunk.ID]
	if !exists {
		entry = &pendingAsset{ext: chunk.Ext, total: chunk.Total, chunks: make(map[uint32][]byte, chunk.Total), origin: origin}
		a.pending[chunk.ID] = entry
	}
	entry.lastChunk = time.Now()
	entry.origin = origin
	if _, has := entry.chunks[chunk.Index]; !has {
		entry.chunks[chunk.Index] = append([]byte(nil), chunk.Data...)
	}
	if entry.total == 0 || uint32(len(entry.chunks)) < entry.total {
		return nil, false
	}
	data := make([]byte, 0, len(entry.chunks)*ChunkDataSize)
	for index := uint32(0); index < entry.total; index++ {
		data = append(data, entry.chunks[index]...)
	}
	delete(a.pending, chunk.ID)
	return data, true
}

// MissingNotice pairs a retry request with the address it should be sent to.
type MissingNotice struct {
	Origin  string
	Request MissingRequest
}

// Sweep returns retry requests for transfers that stalled for longer than
// stallAfter, giving up (and dropping) any that exceeded maxAttempts.
func (a *Assembler) Sweep(stallAfter time.Duration, maxAttempts int) []MissingNotice {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	var notices []MissingNotice
	for id, entry := range a.pending {
		if now.Sub(entry.lastChunk) < stallAfter {
			continue
		}
		if entry.attempts >= maxAttempts {
			delete(a.pending, id)
			continue
		}
		missing := make([]uint32, 0, int(entry.total)-len(entry.chunks))
		for index := uint32(0); index < entry.total; index++ {
			if _, has := entry.chunks[index]; !has {
				missing = append(missing, index)
			}
		}
		if len(missing) == 0 {
			continue
		}
		entry.attempts++
		entry.lastChunk = now
		notices = append(notices, MissingNotice{Origin: entry.origin, Request: MissingRequest{ID: id, Ext: entry.ext, Missing: missing}})
	}
	return notices
}
