package screen

import (
	"net/netip"
	"sync"
	"time"
)

// Ritmo según el enlace. En una LAN el tope de internet (pensado para no tapar la subida del host
// ni la voz) no tiene sentido: una imagen completa de ~700 KB tardaba ~4 s. El host clasifica a
// cada participante como cercano o no con la dirección y los pings que ya hace cada 2 s.
const (
	// NearRTT es la latencia más baja (de la ventana) por debajo de la cual un enlace con IP privada
	// cuenta como LAN. Wi-Fi o cable en la misma casa: 1–5 ms; por internet, aun cerca, más.
	NearRTT = 10 * time.Millisecond
	// rttWindow: el mínimo de las últimas 5 mediciones (~10 s) ignora los picos de cola.
	rttWindow = 5
	// rttMaxAddrs acota las direcciones guardadas (cada reconexión puede traer otra).
	rttMaxAddrs = 64
)

// RTTFloor guarda las últimas latencias por dirección "IP:puerto". El valor cero sirve. Seguro
// para goroutines concurrentes (los pongs llegan en receiveLoop y lo lee pingPeers).
type RTTFloor struct {
	mu      sync.Mutex
	samples map[string]*rttRing
}

type rttRing struct {
	values  [rttWindow]time.Duration
	n, next int
}

// Add anota una latencia medida (ping → pong).
func (r *RTTFloor) Add(addr string, rtt time.Duration) {
	if rtt <= 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.samples == nil || len(r.samples) >= rttMaxAddrs && r.samples[addr] == nil {
		r.samples = make(map[string]*rttRing)
	}
	ring := r.samples[addr]
	if ring == nil {
		ring = &rttRing{}
		r.samples[addr] = ring
	}
	ring.values[ring.next] = rtt
	ring.next = (ring.next + 1) % rttWindow
	ring.n = min(ring.n+1, rttWindow)
}

// Floor es la latencia más baja de las últimas mediciones de addr (false si no hay ninguna).
func (r *RTTFloor) Floor(addr string) (time.Duration, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ring := r.samples[addr]
	if ring == nil || ring.n == 0 {
		return 0, false
	}
	floor := ring.values[0]
	for _, value := range ring.values[1:ring.n] {
		floor = min(floor, value)
	}
	return floor, true
}

// Near dice si addr es un enlace de LAN: IP privada, loopback o link-local y latencia mínima ≤
// NearRTT. Sin mediciones todavía, no (empieza con el ritmo de internet).
func (r *RTTFloor) Near(addr string) bool {
	parsed, err := netip.ParseAddrPort(addr)
	if err != nil {
		return false
	}
	if ip := parsed.Addr().Unmap(); !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() {
		return false
	}
	floor, ok := r.Floor(addr)
	return ok && floor <= NearRTT
}
