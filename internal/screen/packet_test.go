package screen

import (
	"bytes"
	"testing"
)

var testSharer = PeerID{0xde, 0xad, 0xbe, 0xef, 0, 1, 2, 3}

func TestPeerIDRoundTrip(t *testing.T) {
	id, ok := ParsePeerID("deadbeef00010203")
	if !ok || id != testSharer || id.String() != "deadbeef00010203" {
		t.Fatalf("ParsePeerID = %v, %v", id, ok)
	}
	for _, bad := range []string{"", "deadbeef", "zzzzzzzzzzzzzzzz", "deadbeef0001020304"} {
		if _, ok := ParsePeerID(bad); ok {
			t.Fatalf("ParsePeerID(%q) accepted", bad)
		}
	}
}

func TestChunkRoundTrip(t *testing.T) {
	png := bytes.Repeat([]byte{7}, 2*ChunkDataSize+10)
	header := TileChunk{Sharer: testSharer, Session: 42, Width: 1920, Height: 1080, Tile: 509, Serial: 9}
	var joined []byte
	for index := range ChunkCount(len(png)) {
		payload := AppendChunk(nil, header, png, index)
		if len(payload) > HeaderSize+ChunkDataSize {
			t.Fatalf("chunk %d has %d bytes", index, len(payload))
		}
		c, err := DecodeChunk(payload)
		if err != nil {
			t.Fatal(err)
		}
		if c.Sharer != testSharer || c.Session != 42 || c.Width != 1920 || c.Height != 1080 || c.Tile != 509 || c.Serial != 9 || c.Index != index || c.Total != 3 {
			t.Fatalf("decoded %+v", c)
		}
		joined = append(joined, c.Data...)
	}
	if !bytes.Equal(joined, png) {
		t.Fatal("the chunks do not rebuild the PNG")
	}
}

func TestDecodeChunkRejectsOutOfBounds(t *testing.T) {
	valid := TileChunk{Sharer: testSharer, Session: 1, Width: 1920, Height: 1080, Tile: 0, Serial: 1}
	data := []byte{1, 2, 3}
	if _, err := DecodeChunk(AppendChunk(nil, valid, data, 0)); err != nil {
		t.Fatalf("valid chunk rejected: %v", err)
	}
	cases := map[string]func(p []byte){
		"tile outside the grid": func(p []byte) { p[16], p[17] = 0x02, 0x00 }, // 512 > 510 tiles
		"zero width":            func(p []byte) { p[12], p[13] = 0, 0 },
		"too wide":              func(p []byte) { p[12], p[13] = 0xff, 0xff },
		"too many chunks":       func(p []byte) { p[23] = MaxTileChunks + 1 },
		"index past total":      func(p []byte) { p[22], p[23] = 3, 3 },
		"serial zero":           func(p []byte) { p[18], p[19], p[20], p[21] = 0, 0, 0, 0 },
	}
	for name, corrupt := range cases {
		payload := AppendChunk(nil, valid, data, 0)
		corrupt(payload)
		if _, err := DecodeChunk(payload); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := DecodeChunk(make([]byte, HeaderSize)); err == nil {
		t.Error("a chunk without data must be rejected")
	}
	if _, err := DecodeChunk(make([]byte, HeaderSize+ChunkDataSize+1)); err == nil {
		t.Error("a chunk bigger than ChunkDataSize must be rejected")
	}
}
