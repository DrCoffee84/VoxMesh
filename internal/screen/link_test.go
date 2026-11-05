package screen

import (
	"fmt"
	"testing"
	"time"
)

func TestRTTFloorNearNeedsPrivateAddressAndLowPing(t *testing.T) {
	var r RTTFloor
	for _, addr := range []string{"192.168.1.20:47830", "10.0.0.5:47830", "127.0.0.1:47830", "186.57.0.1:47830", "100.101.102.103:47830", "26.114.3.224:47830"} {
		r.Add(addr, 2*time.Millisecond)
	}
	cases := map[string]bool{
		"192.168.1.20:47830":    true,
		"10.0.0.5:47830":        true,
		"127.0.0.1:47830":       true,
		"186.57.0.1:47830":      false, // pública: internet aunque el ping sea bajo
		"100.101.102.103:47830": false, // Tailscale (CGNAT): VPN por internet
		"26.114.3.224:47830":    false, // Radmin VPN
		"192.168.1.99:47830":    false, // sin mediciones todavía
		"no es una dirección":   false,
	}
	for addr, want := range cases {
		if got := r.Near(addr); got != want {
			t.Errorf("Near(%q) = %v, want %v", addr, got, want)
		}
	}
}

func TestRTTFloorIgnoresSpikes(t *testing.T) {
	var r RTTFloor
	addr := "192.168.1.20:47830"
	for _, ms := range []int{3, 80, 4, 120, 5} { // Wi-Fi: picos de cola
		r.Add(addr, time.Duration(ms)*time.Millisecond)
	}
	if !r.Near(addr) {
		t.Fatal("a LAN with ping spikes is still a LAN")
	}
	for range rttWindow {
		r.Add(addr, 30*time.Millisecond) // cinco mediciones lentas seguidas: ya no
	}
	if r.Near(addr) {
		t.Fatal("a private address that answers in 30 ms is not a LAN (VPN through the internet)")
	}
	if floor, _ := r.Floor(addr); floor != 30*time.Millisecond {
		t.Fatalf("floor = %v", floor)
	}
}

func TestRTTFloorIsBounded(t *testing.T) {
	var r RTTFloor
	for i := range rttMaxAddrs * 3 {
		r.Add(fmt.Sprintf("192.168.1.%d:%d", i%250, 40000+i), time.Millisecond)
	}
	if len(r.samples) > rttMaxAddrs {
		t.Fatalf("%d addresses kept, cap %d", len(r.samples), rttMaxAddrs)
	}
}
