package screen

import (
	"bytes"
	"testing"
	"time"
)

var asmT0 = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

// chunksOf parte png en los chunks decodificados de un tile.
func chunksOf(t *testing.T, header TileChunk, png []byte) []TileChunk {
	t.Helper()
	var out []TileChunk
	for index := range ChunkCount(len(png)) {
		c, err := DecodeChunk(AppendChunk(nil, header, png, index))
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, c)
	}
	return out
}

func tileHeader(session uint32, tile int, serial uint32) TileChunk {
	return TileChunk{Sharer: testSharer, Session: session, Width: 1920, Height: 1080, Tile: tile, Serial: serial}
}

func TestAssemblerRebuildsOutOfOrder(t *testing.T) {
	a := NewAssembler()
	png := bytes.Repeat([]byte("abc"), ChunkDataSize) // 3 chunks
	chunks := chunksOf(t, tileHeader(5, 3, 1), png)
	if r := a.Add(chunks[2], asmT0); !r.Reset || r.PNG != nil {
		t.Fatalf("first chunk of a session must reset and not complete: %+v", r)
	}
	if r := a.Add(chunks[0], asmT0); r.PNG != nil || r.Reset {
		t.Fatal("incomplete tile returned")
	}
	r := a.Add(chunks[1], asmT0)
	if r.Tile != 3 || !bytes.Equal(r.PNG, png) {
		t.Fatalf("tile 3 not rebuilt: tile=%d len=%d", r.Tile, len(r.PNG))
	}
	if r := a.Add(chunks[1], asmT0); r.PNG != nil {
		t.Fatal("a duplicated chunk of an applied tile must be ignored")
	}
}

// Un chunk perdido deja el tile como estaba; el serial siguiente lo reemplaza sin mezclarse.
func TestAssemblerLostChunkKeepsTheOldTile(t *testing.T) {
	a := NewAssembler()
	first := bytes.Repeat([]byte{1}, 2*ChunkDataSize)
	second := bytes.Repeat([]byte{2}, 2*ChunkDataSize)
	old := chunksOf(t, tileHeader(5, 0, 1), first)
	a.Add(old[0], asmT0) // el chunk 1 se perdió
	next := chunksOf(t, tileHeader(5, 0, 2), second)
	a.Add(next[0], asmT0)
	if r := a.Add(old[1], asmT0); r.PNG != nil {
		t.Fatal("a chunk of the replaced serial must not complete anything")
	}
	r := a.Add(next[1], asmT0)
	if !bytes.Equal(r.PNG, second) {
		t.Fatal("serial 2 must rebuild only with its own chunks")
	}
	if a.Incomplete != 1 {
		t.Fatalf("Incomplete = %d, want 1 (serial 1 never completed)", a.Incomplete)
	}
}

func TestAssemblerOlderSerialAfterNewer(t *testing.T) {
	a := NewAssembler()
	newer := chunksOf(t, tileHeader(5, 7, 9), []byte("nuevo"))
	older := chunksOf(t, tileHeader(5, 7, 4), []byte("viejo"))
	if r := a.Add(newer[0], asmT0); string(r.PNG) != "nuevo" {
		t.Fatal("serial 9 must complete")
	}
	if r := a.Add(older[0], asmT0); r.PNG != nil {
		t.Fatal("an older serial arriving late must not overwrite the newer tile")
	}
}

func TestAssemblerNewSessionResets(t *testing.T) {
	a := NewAssembler()
	a.Add(chunksOf(t, tileHeader(5, 0, 7), []byte("x"))[0], asmT0)
	// Volvió a compartir: sesión nueva, el serial empieza de nuevo y el tamaño puede ser otro.
	c := chunksOf(t, TileChunk{Sharer: testSharer, Session: 6, Width: 1280, Height: 720, Tile: 0, Serial: 1}, []byte("y"))[0]
	r := a.Add(c, asmT0)
	if !r.Reset || r.Grid.W != 1280 || string(r.PNG) != "y" {
		t.Fatalf("new session: %+v", r)
	}
	// Un chunk atrasado de la sesión anterior no vuelve a resetear.
	if r := a.Add(chunksOf(t, tileHeader(5, 1, 8), []byte("z"))[0], asmT0); r.Reset || r.PNG != nil {
		t.Fatalf("late chunk of a retired session: %+v", r)
	}
	// Mismo número de sesión con otro tamaño: se descarta.
	if r := a.Add(chunksOf(t, tileHeader(6, 1, 2), []byte("w"))[0], asmT0); r.PNG != nil || r.Reset {
		t.Fatal("a session has a single frame size")
	}
}

func TestAssemblerSweepDropsStalled(t *testing.T) {
	a := NewAssembler()
	png := bytes.Repeat([]byte{3}, 2*ChunkDataSize)
	a.Add(chunksOf(t, tileHeader(5, 0, 1), png)[0], asmT0)
	if a.Sweep(asmT0.Add(time.Second)) != 0 {
		t.Fatal("one second is not stalled yet")
	}
	if a.Sweep(asmT0.Add(PendingTTL+time.Millisecond)) != 1 || a.Incomplete != 1 {
		t.Fatal("a tile stalled for more than PendingTTL must be dropped")
	}
}

func TestAssemblerBoundsPendingTiles(t *testing.T) {
	a := NewAssembler()
	png := bytes.Repeat([]byte{4}, 2*ChunkDataSize)
	for tile := range MaxPending + 50 {
		a.Add(chunksOf(t, TileChunk{Sharer: testSharer, Session: 5, Width: 7680, Height: 4320, Tile: tile, Serial: uint32(tile + 1)}, png)[0], asmT0)
	}
	if len(a.pending) != MaxPending {
		t.Fatalf("pending tiles = %d, want the cap %d", len(a.pending), MaxPending)
	}
}
