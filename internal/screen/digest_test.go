package screen

import (
	"image"
	"slices"
	"testing"
	"time"
)

func TestDigestAndRepairRoundTrip(t *testing.T) {
	d := Digest{Sharer: testSharer, Session: 9, Width: 1920, Height: 1080, First: 100, Serials: []uint16{0, 1, 65535}}
	got, err := DecodeDigest(AppendDigest(nil, d))
	if err != nil || got.Session != 9 || got.First != 100 || !slices.Equal(got.Serials, d.Serials) || got.Sharer != testSharer {
		t.Fatalf("digest = %+v, err %v", got, err)
	}
	r := Repair{Sharer: testSharer, Session: 9, Tiles: []int{0, 509}}
	gotR, err := DecodeRepair(AppendRepair(nil, r))
	if err != nil || !slices.Equal(gotR.Tiles, r.Tiles) || gotR.Session != 9 {
		t.Fatalf("repair = %+v, err %v", gotR, err)
	}
}

func TestDigestAndRepairBounds(t *testing.T) {
	tooMany := Digest{Sharer: testSharer, Session: 1, Width: 1920, Height: 1080, First: 0, Serials: make([]uint16, DigestMaxTiles+1)}
	if _, err := DecodeDigest(AppendDigest(nil, tooMany)); err == nil {
		t.Error("a digest with more than DigestMaxTiles tiles must be rejected")
	}
	pastGrid := Digest{Sharer: testSharer, Session: 1, Width: 1920, Height: 1080, First: 500, Serials: make([]uint16, 20)}
	if _, err := DecodeDigest(AppendDigest(nil, pastGrid)); err == nil {
		t.Error("a digest past the last tile must be rejected")
	}
	truncated := AppendDigest(nil, Digest{Sharer: testSharer, Session: 1, Width: 640, Height: 480, Serials: []uint16{1, 2}})
	if _, err := DecodeDigest(truncated[:len(truncated)-1]); err == nil {
		t.Error("a truncated digest must be rejected")
	}
	if _, err := DecodeRepair(AppendRepair(nil, Repair{Sharer: testSharer, Tiles: make([]int, DigestMaxTiles+1)})); err == nil {
		t.Error("a repair with more than DigestMaxTiles tiles must be rejected")
	}
	if _, err := DecodeRepair(AppendRepair(nil, Repair{Sharer: testSharer})); err == nil {
		t.Error("an empty repair must be rejected")
	}
}

func TestStaleFindsMissingTilesButNotInFlight(t *testing.T) {
	a := NewAssembler()
	// Tile 0 llegó (serial 1); el 1 se perdió entero (serial 2); el 2 está a medio llegar (serial 3).
	a.Add(chunksOf(t, tileHeader(5, 0, 1), []byte("a"))[0], asmT0)
	a.Add(chunksOf(t, tileHeader(5, 2, 3), make([]byte, 2*ChunkDataSize))[0], asmT0)
	d := Digest{Sharer: testSharer, Session: 5, Width: 1920, Height: 1080, First: 0, Serials: []uint16{1, 2, 3, 0}}
	if got := a.Stale(d); !slices.Equal(got, []int{1}) {
		t.Fatalf("stale = %v, want [1] (tile 2 is still arriving, tile 3 was never sent)", got)
	}
	a.Sweep(asmT0.Add(PendingTTL + time.Second)) // el chunk que faltaba del tile 2 no llegó nunca
	if got := a.Stale(d); !slices.Equal(got, []int{1, 2}) {
		t.Fatalf("stale after the sweep = %v, want [1 2]", got)
	}
	other := d
	other.Session = 6
	if got := a.Stale(other); got != nil {
		t.Fatalf("a digest of another session asks for %v", got)
	}
}

// De punta a punta: se pierden los chunks de dos tiles, el resumen los delata, quien mira los pide
// y en la vuelta siguiente quien comparte los manda: quien mira queda igual a la pantalla.
func TestSenderRepairsLostTiles(t *testing.T) {
	still := noise(64*4, 64*2, 12)
	v := newViewer(t)
	clock := &fakeClock{now: policyT0}
	lost := map[int]bool{3: true, 6: true}
	dropping := true
	var digests [][]byte
	s := &Sender{
		ID:  testSharer,
		Now: clock.Now,
		Emit: func(chunk []byte) bool {
			if c, err := DecodeChunk(chunk); err == nil && dropping && lost[c.Tile] {
				return true // se perdió en el camino
			}
			v.receive(chunk)
			return true
		},
		EmitDigest: func(digest []byte) bool {
			digests = append(digests, append([]byte(nil), digest...))
			return true
		},
	}
	src := &fakeSource{frames: []*image.RGBA{still}}
	if err := s.Step(src, []string{"juan"}, clock.now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	dropping = false
	if len(digests) != 1 {
		t.Fatalf("digests after the full image = %d, want 1", len(digests))
	}
	d, err := DecodeDigest(digests[0])
	if err != nil {
		t.Fatal(err)
	}
	stale := v.asm.Stale(d)
	if !slices.Equal(stale, []int{3, 6}) {
		t.Fatalf("stale = %v, want [3 6]", stale)
	}
	repair, err := DecodeRepair(AppendRepair(nil, Repair{Sharer: testSharer, Session: d.Session, Tiles: stale}))
	if err != nil {
		t.Fatal(err)
	}
	if n := s.MarkTiles(repair.Session, repair.Tiles); n != 2 {
		t.Fatalf("marked %d tiles, want 2", n)
	}
	before := len(v.tiles)
	if err := s.Step(src, []string{"juan"}, clock.now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := v.tiles[before:]; !slices.Equal(got, []int{3, 6}) {
		t.Fatalf("resent tiles = %v, want only [3 6]", got)
	}
	if string(v.frame.Pix) != string(still.Pix) {
		t.Fatal("the viewer is not identical to the screen after the repair")
	}
	if s.MarkTiles(d.Session+1, []int{0}) != 0 {
		t.Fatal("a repair of another session must be ignored")
	}
	// El siguiente resumen sale DigestInterval después, no en cada vuelta.
	if err := s.Step(src, []string{"juan"}, clock.now.Add(time.Hour)); err != nil || len(digests) != 1 {
		t.Fatalf("digests = %d right after the previous one (err %v)", len(digests), err)
	}
	clock.now = clock.now.Add(DigestInterval)
	if err := s.Step(src, []string{"juan"}, clock.now.Add(time.Hour)); err != nil || len(digests) != 2 {
		t.Fatalf("digests = %d after DigestInterval (err %v)", len(digests), err)
	}
}
