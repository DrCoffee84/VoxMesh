package screen

import (
	"image"
	"image/color"
	"slices"
	"testing"
	"time"
)

// solid es un cuadro de w×h de un solo color.
func solid(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, c.A
	}
	return img
}

func TestGridEdgesArePartialTiles(t *testing.T) {
	g := NewGrid(1366, 768)
	if g.Cols != 22 || g.Rows != 12 || g.Count() != 264 {
		t.Fatalf("grid = %+v", g)
	}
	if last := g.Rect(21); last != image.Rect(1344, 0, 1366, 64) {
		t.Fatalf("right edge tile = %v", last)
	}
	if corner := g.Rect(g.Count() - 1); corner != image.Rect(1344, 704, 1366, 768) {
		t.Fatalf("bottom-right tile = %v", corner)
	}
}

func TestDiffFindsASinglePixel(t *testing.T) {
	g := NewGrid(1366, 768)
	cur, sent := solid(1366, 768, color.RGBA{10, 20, 30, 255}), solid(1366, 768, color.RGBA{10, 20, 30, 255})
	changed := make([]bool, g.Count())
	Diff(cur, sent, g, changed)
	if slices.Contains(changed, true) {
		t.Fatal("identical frames must have no changed tiles")
	}
	cur.SetRGBA(1365, 767, color.RGBA{11, 20, 30, 255}) // último píxel del tile parcial de la esquina
	cur.SetRGBA(64, 0, color.RGBA{0, 0, 0, 255})        // primer píxel del segundo tile
	Diff(cur, sent, g, changed)
	var got []int
	for i, c := range changed {
		if c {
			got = append(got, i)
		}
	}
	if !slices.Equal(got, []int{1, g.Count() - 1}) {
		t.Fatalf("changed tiles = %v, want [1 %d]", got, g.Count()-1)
	}
}

func TestSchedulerOldestChangeFirst(t *testing.T) {
	g := NewGrid(64*4, 64)
	s := NewScheduler(g)
	s.Update([]bool{false, false, true, false}) // el 2 cambia primero
	s.Update([]bool{true, false, true, true})   // después el 0 y el 3
	if got := s.Order(); !slices.Equal(got, []int{2, 0, 3}) {
		t.Fatalf("order = %v, want [2 0 3]", got)
	}
	s.Sent(2)
	s.Update([]bool{true, false, false, true}) // el 2 volvió a lo enviado
	if got := s.Order(); !slices.Equal(got, []int{0, 3}) {
		t.Fatalf("order = %v, want [0 3]", got)
	}
}

// Una zona que cambia en cada captura no deja sin turno al resto: lo que espera desde antes sale
// primero aunque se manden pocos tiles por vuelta.
func TestSchedulerDoesNotStarveTiles(t *testing.T) {
	g := NewGrid(64*10, 64)
	s := NewScheduler(g)
	pending := map[int]bool{}
	for i := range g.Count() {
		pending[i] = true
	}
	for round := range 30 {
		changed := make([]bool, g.Count())
		for i := range changed {
			changed[i] = pending[i] || i == 0 // el tile 0 cambia siempre (un video)
		}
		s.Update(changed)
		for _, tile := range s.Order()[:min(2, len(s.Order()))] { // entran 2 por vuelta
			s.Sent(tile)
			delete(pending, tile)
		}
		if len(pending) == 0 {
			if round > 10 {
				t.Fatalf("took %d rounds to send every tile once", round)
			}
			return
		}
	}
	t.Fatalf("tiles never sent: %v", pending)
}

func TestSchedulerFullImage(t *testing.T) {
	g := NewGrid(64*3, 64)
	s := NewScheduler(g)
	s.Update([]bool{false, true, false})
	s.MarkAll()
	if !s.Refreshing() {
		t.Fatal("MarkAll must start a full image")
	}
	if got := s.Order(); !slices.Equal(got, []int{1, 0, 2}) {
		t.Fatalf("order = %v, want the changed tile first, then the rest", got)
	}
	for _, tile := range []int{1, 0, 2} {
		s.Sent(tile)
	}
	if s.Refreshing() || len(s.Order()) != 0 {
		t.Fatal("after sending every tile the full image is done")
	}
}

var policyT0 = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

func TestRefreshPolicyNewWatcher(t *testing.T) {
	var p RefreshPolicy
	if !p.Due([]string{"a"}, 1920, 1080) {
		t.Fatal("the first watcher gets a full image")
	}
	if p.Due([]string{"a"}, 1920, 1080) {
		t.Fatal("nothing new: no full image (losses are repaired, not refreshed)")
	}
	if !p.Due([]string{"a", "b"}, 1920, 1080) {
		t.Fatal("a second watcher gets a full image")
	}
	if p.Due([]string{"b"}, 1920, 1080) {
		t.Fatal("someone leaving needs no full image")
	}
	if !p.Due([]string{"a", "b"}, 1920, 1080) {
		t.Fatal("whoever comes back has nothing: full image")
	}
}

func TestRefreshPolicySizeChange(t *testing.T) {
	var p RefreshPolicy
	p.Due([]string{"a"}, 1920, 1080)
	if !p.Due([]string{"a"}, 1280, 720) {
		t.Fatal("a new frame size needs a full image")
	}
}

func TestRefreshPolicyIdleWithoutWatchers(t *testing.T) {
	var p RefreshPolicy
	p.Due([]string{"a"}, 1920, 1080)
	if p.Due(nil, 0, 0) {
		t.Fatal("no full image with nobody watching")
	}
	if !p.Due([]string{"a"}, 1920, 1080) {
		t.Fatal("whoever starts watching again gets a full image")
	}
}

func TestSchedulerMark(t *testing.T) {
	s := NewScheduler(NewGrid(64*3, 64))
	if !s.Mark(2) || s.Mark(2) {
		t.Fatal("Mark reports whether the tile was newly marked")
	}
	if got := s.Order(); !slices.Equal(got, []int{2}) || !s.Refreshing() {
		t.Fatalf("order = %v", got)
	}
	s.Sent(2)
	if s.Refreshing() {
		t.Fatal("a marked tile that went out is done")
	}
}
