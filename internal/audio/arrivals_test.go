package audio

import (
	"path/filepath"
	"testing"
	"time"
)

func TestArrivalRecorder_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "llegadas.csv")
	rec, err := NewArrivalRecorder(path)
	if err != nil {
		t.Fatal(err)
	}
	start := rec.start
	rec.Record("829f6c20879e92ff", 10, 3, start.Add(20*time.Millisecond))
	rec.Record("fc12cbcabe706739", 7, 1, start.Add(25*time.Millisecond))
	rec.Record("829f6c20879e92ff", 11, 3, start.Add(41*time.Millisecond))
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}
	rec.Record("829f6c20879e92ff", 12, 3, start) // después de Close se ignora, sin pánico

	bySender, err := readArrivals(path)
	if err != nil {
		t.Fatal(err)
	}
	got := bySender["829f6c20879e92ff"]
	if len(got) != 2 || len(bySender["fc12cbcabe706739"]) != 1 {
		t.Fatalf("unexpected arrivals: %+v", bySender)
	}
	if got[1].seq != 11 || got[1].talkspurt != 3 || got[1].arriveAt != 41*time.Millisecond {
		t.Fatalf("arrival not preserved: %+v", got[1])
	}
}
