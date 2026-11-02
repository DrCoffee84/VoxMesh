//go:build windows

package winproc

import (
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestConfigure(t *testing.T) {
	summary := Configure()
	t.Log(summary)
	class, err := windows.GetPriorityClass(windows.CurrentProcess())
	if err != nil {
		t.Fatal(err)
	}
	if class != windows.ABOVE_NORMAL_PRIORITY_CLASS {
		t.Fatalf("prioridad = %#x, quería ABOVE_NORMAL", class)
	}
	if !strings.Contains(summary, "sin power throttling") {
		t.Fatalf("no se pudo sacar el power throttling: %s", summary)
	}
}

func TestKeepSystemAwake(t *testing.T) {
	for _, on := range []bool{true, true, false, true, false} {
		if err := KeepSystemAwake(on); err != nil {
			t.Fatalf("KeepSystemAwake(%v): %v", on, err)
		}
	}
}
