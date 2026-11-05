package screen

import (
	"testing"
	"time"
)

var cadT0 = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

func TestNextCapture(t *testing.T) {
	if got := NextCapture(time.Time{}, cadT0); got != CaptureIdle {
		t.Fatalf("never changed: %v, want idle", got)
	}
	if got := NextCapture(cadT0.Add(-time.Second), cadT0); got != CaptureFast {
		t.Fatalf("changed 1 s ago: %v, want fast", got)
	}
	if got := NextCapture(cadT0.Add(-IdleAfter), cadT0); got != CaptureIdle {
		t.Fatalf("changed %v ago: %v, want idle", IdleAfter, got)
	}
}

func TestPaintDue(t *testing.T) {
	ms := func(n int) time.Time { return cadT0.Add(time.Duration(n) * time.Millisecond) }
	lastPaint := ms(-1000)
	cases := []struct {
		name                  string
		dirtySince, lastChunk time.Time
		lastPaint, now        time.Time
		want                  bool
	}{
		{"nada que pintar", time.Time{}, ms(0), lastPaint, ms(500), false},
		{"tanda terminada", ms(0), ms(10), lastPaint, ms(40), true},
		{"siguen llegando chunks", ms(0), ms(90), lastPaint, ms(100), false},
		{"chunks seguidos pero ya esperó mucho", ms(0), ms(119), lastPaint, ms(120), true},
		{"pintó hace poco", ms(0), ms(0), ms(-30), ms(40), false},
		{"pintó hace bastante", ms(0), ms(0), ms(-40), ms(40), true},
	}
	for _, c := range cases {
		if got := PaintDue(c.dirtySince, c.lastChunk, c.lastPaint, c.now); got != c.want {
			t.Errorf("%s: PaintDue = %v, want %v", c.name, got, c.want)
		}
	}
}
