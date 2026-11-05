package screen

import (
	"image"
	"testing"
)

func TestBGRAToRGBAIsOpaque(t *testing.T) {
	pix := []byte{1, 2, 3, 0, 10, 20, 30, 7}
	bgraToRGBA(pix)
	if want := []byte{3, 2, 1, 255, 30, 20, 10, 255}; string(pix) != string(want) {
		t.Fatalf("pix = %v, want %v", pix, want)
	}
}

func TestPickMonitor(t *testing.T) {
	monitors := []Monitor{
		{Number: 1, Device: `\\.\DISPLAY2`, Rect: image.Rect(-1920, 0, 0, 1080)},
		{Number: 2, Device: `\\.\DISPLAY1`, Rect: image.Rect(0, 0, 2560, 1440), Primary: true},
	}
	if got := PickMonitor(monitors, ""); got.Device != `\\.\DISPLAY1` {
		t.Fatalf("default = %s, want the primary", got.Device)
	}
	if got := PickMonitor(monitors, `\\.\DISPLAY2`); got.Number != 1 {
		t.Fatalf("chosen = %+v", got)
	}
	if got := PickMonitor(monitors, `\\.\DISPLAY9`); !got.Primary {
		t.Fatal("a monitor that went away falls back to the primary")
	}
}
