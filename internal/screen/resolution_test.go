package screen

import (
	"image"
	"image/color"
	"slices"
	"testing"
	"time"
)

func TestParseResolution(t *testing.T) {
	for stored, want := range map[string]Resolution{
		"full": ResolutionFull, "1080p": Resolution1080, "900p": Resolution900, "720p": Resolution720,
		"": DefaultResolution, "4k": DefaultResolution, "FULL": DefaultResolution,
	} {
		if got := ParseResolution(stored); got != want {
			t.Errorf("ParseResolution(%q) = %q, want %q", stored, got, want)
		}
	}
	if DefaultResolution.MaxWidth() != DefaultMaxWidth {
		t.Fatalf("the default must keep today's limit (%d), got %d", DefaultMaxWidth, DefaultResolution.MaxWidth())
	}
}

// "Completa" solo para monitores de más de 1920; la preferida que ese monitor no ofrece se muestra
// como 1080p.
func TestResolutionsOffered(t *testing.T) {
	if got := Resolutions(1920); slices.Contains(got, ResolutionFull) || len(got) != 3 {
		t.Fatalf("1920 wide: %v, want 1080p 900p 720p", got)
	}
	if got := Resolutions(2560); got[0] != ResolutionFull || len(got) != 4 {
		t.Fatalf("2560 wide: %v, want full first", got)
	}
	if got := OfferedResolution(ResolutionFull, 1920); got != Resolution1080 {
		t.Fatalf("full on a 1920 monitor shows %q, want 1080p", got)
	}
	if got := OfferedResolution(ResolutionFull, 2560); got != ResolutionFull {
		t.Fatalf("full on a 2560 monitor shows %q", got)
	}
	if got := OfferedResolution(Resolution720, 1366); got != Resolution720 {
		t.Fatalf("720p on a 1366 monitor shows %q", got)
	}
}

// Opción → tamaño del cuadro que sale: se achica al ancho de la opción conservando la proporción y
// un monitor más chico que la opción sale a su tamaño (no se agranda).
func TestResolutionFrameSize(t *testing.T) {
	cases := []struct {
		monitorW, monitorH int
		res                Resolution
		wantW, wantH       int
	}{
		{2560, 1440, ResolutionFull, 2560, 1440},
		{2560, 1440, Resolution1080, 1920, 1080},
		{2560, 1440, Resolution900, 1600, 900},
		{2560, 1440, Resolution720, 1280, 720},
		{1920, 1080, Resolution900, 1600, 900},
		{1600, 900, Resolution1080, 1600, 900},  // más chico que la opción
		{1280, 1024, Resolution900, 1280, 1024}, // igual de ancho que 720p: queda
		{1366, 768, Resolution1080, 1366, 768},
	}
	for _, c := range cases {
		s := &Sender{ID: testSharer, MaxWidth: c.res.MaxWidth(), Emit: func([]byte) bool { return true }}
		src := &fakeSource{frames: []*image.RGBA{solid(c.monitorW, c.monitorH, color.RGBA{9, 9, 9, 255})}}
		if err := s.Step(src, []string{"juan"}, time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		if w, h := s.Size(); w != c.wantW || h != c.wantH {
			t.Errorf("%d×%d at %s: frame %d×%d, want %d×%d", c.monitorW, c.monitorH, c.res, w, h, c.wantW, c.wantH)
		}
	}
}
