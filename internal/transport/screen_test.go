package transport

import (
	"net"
	"testing"
	"time"
)

// Cola de la pantalla compartida: ritmo propio, tope sumando destinos, descarte sin esperar en el
// reenvío y separada de la de archivos.

func listenPair(t *testing.T) (*UDP, *net.UDPConn) {
	t.Helper()
	sender, err := Listen("127.0.0.1:0", testKey)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sender.Close() })
	receiver, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = receiver.Close() })
	return sender, receiver
}

// countArrivals lee datagramas hasta que llegan want o pasa timeout, y devuelve la hora del último.
func countArrivals(t *testing.T, conn *net.UDPConn, want int, timeout time.Duration) (int, time.Time) {
	t.Helper()
	buf := make([]byte, 2048)
	var last time.Time
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	for got := 0; got < want; got++ {
		if _, _, err := conn.ReadFromUDP(buf); err != nil {
			return got, last
		}
		last = time.Now()
	}
	return want, last
}

// Dos destinos cuentan doble: el host que reenvía a varios no pasa de ScreenRateBytes en total.
func TestUDP_ScreenIsPacedSummingTargets(t *testing.T) {
	sender, receiver := listenPair(t)
	second, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	targets := []*net.UDPAddr{receiver.LocalAddr().(*net.UDPAddr), second.LocalAddr().(*net.UDPAddr)}

	const packets = 40
	payload := make([]byte, 1200)
	start := time.Now()
	go func() {
		for i := 0; i < packets; i++ {
			sender.SendScreen(targets, Packet{Kind: PacketScreenTile, Payload: payload}, nil)
		}
	}()
	go func() { countArrivals(t, second, packets, 5*time.Second) }()
	got, last := countArrivals(t, receiver, packets, 5*time.Second)
	if got != packets {
		t.Fatalf("received %d of %d screen packets", got, packets)
	}
	total := 2 * packets * sealedSize(len(payload))
	minimum := time.Duration(float64(total-screenBurstBytes) / ScreenRateBytes * float64(time.Second))
	if elapsed := last.Sub(start); elapsed < minimum*9/10 {
		t.Fatalf("screen lane not paced: %d bytes in %v, want at least %v", total, elapsed, minimum)
	}
	if sent, _ := sender.ScreenCounters(); sent < uint64(total)-uint64(2*sealedSize(len(payload))) {
		t.Fatalf("ScreenCounters sent = %d, want about %d", sent, total)
	}
}

// Quien comparte desde un cliente baja la tasa (presupuesto del host / copias).
func TestUDP_SetScreenRateSlowsTheLane(t *testing.T) {
	sender, receiver := listenPair(t)
	sender.SetScreenRate(ScreenRateBytes / 4)
	targets := []*net.UDPAddr{receiver.LocalAddr().(*net.UDPAddr)}
	const packets = 20
	payload := make([]byte, 1200)
	start := time.Now()
	go func() {
		for i := 0; i < packets; i++ {
			sender.SendScreen(targets, Packet{Kind: PacketScreenTile, Payload: payload}, nil)
		}
	}()
	got, last := countArrivals(t, receiver, packets, 5*time.Second)
	if got != packets {
		t.Fatalf("received %d of %d", got, packets)
	}
	total := packets * sealedSize(len(payload))
	minimum := time.Duration(float64(total-screenBurstBytes) / (ScreenRateBytes / 4) * float64(time.Second))
	if elapsed := last.Sub(start); elapsed < minimum*9/10 {
		t.Fatalf("SetScreenRate ignored: %d bytes in %v, want at least %v", total, elapsed, minimum)
	}
}

func TestUDP_SetScreenRateIsClamped(t *testing.T) {
	sender, _ := listenPair(t)
	sender.SetScreenRate(10 * ScreenLANRateBytes)
	if got := sender.ScreenRate(); got != ScreenLANRateBytes {
		t.Fatalf("rate = %d, want the LAN cap %d", got, ScreenLANRateBytes)
	}
	sender.SetScreenRate(0)
	if got := sender.screen.rate.Load(); got != screenMinRate {
		t.Fatalf("rate = %d, want the floor %d", got, screenMinRate)
	}
}

// El reenvío del host nunca espera (corre en receiveLoop): con la cola llena descarta y cuenta.
func TestUDP_TryRelayScreenDropsWhenFull(t *testing.T) {
	sender, receiver := listenPair(t)
	sender.SetScreenRate(screenMinRate) // casi no sale nada: la cola se llena enseguida
	targets := []*net.UDPAddr{receiver.LocalAddr().(*net.UDPAddr)}
	payload := make([]byte, 1200)
	start := time.Now()
	accepted := 0
	for i := 0; i < screenQueue*3; i++ {
		if sender.TryRelayScreen(Packet{Kind: PacketScreenTile, Payload: payload}, targets) {
			accepted++
		}
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("TryRelayScreen waited %v", elapsed)
	}
	if accepted > screenQueue+1 {
		t.Fatalf("accepted %d packets, the queue holds %d", accepted, screenQueue)
	}
	if _, dropped := sender.ScreenCounters(); dropped != uint64(screenQueue*3-accepted) {
		t.Fatalf("dropped = %d, want %d", dropped, screenQueue*3-accepted)
	}
}

// Una sincronización de sonidos que llena la cola de archivos no frena la pantalla.
func TestUDP_FullBulkDoesNotBlockScreen(t *testing.T) {
	sender, receiver := listenPair(t)
	dead := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9}
	payload := make([]byte, 1200)
	go func() {
		for i := 0; i < bulkQueue*4; i++ {
			if !sender.SendBulk(dead, Packet{Kind: PacketAssetChunk, Payload: payload}) {
				return
			}
		}
	}()
	time.Sleep(50 * time.Millisecond) // la cola de archivos ya está llena
	targets := []*net.UDPAddr{receiver.LocalAddr().(*net.UDPAddr)}
	done := make(chan bool, 1)
	go func() { done <- sender.SendScreen(targets, Packet{Kind: PacketScreenTile, Payload: payload}, nil) }()
	select {
	case ok := <-done:
		if !ok {
			t.Fatal("SendScreen returned false on an open socket")
		}
	case <-time.After(time.Second):
		t.Fatal("SendScreen waited behind the bulk queue")
	}
	if got, _ := countArrivals(t, receiver, 1, 2*time.Second); got != 1 {
		t.Fatal("the screen packet did not arrive while the bulk queue was full")
	}
}

// Detener la pantalla destraba a quien espera lugar en la cola.
func TestUDP_SendScreenStops(t *testing.T) {
	sender, _ := listenPair(t)
	sender.SetScreenRate(screenMinRate)
	dead := []*net.UDPAddr{{IP: net.IPv4(127, 0, 0, 1), Port: 9}}
	payload := make([]byte, 1200)
	stop := make(chan struct{})
	done := make(chan bool, 1)
	go func() {
		for i := 0; i < screenQueue*4; i++ {
			if !sender.SendScreen(dead, Packet{Kind: PacketScreenTile, Payload: payload}, stop) {
				done <- false
				return
			}
		}
		done <- true
	}()
	time.Sleep(100 * time.Millisecond)
	close(stop)
	select {
	case finished := <-done:
		if finished {
			t.Fatal("every packet was queued: the screen queue is not bounded")
		}
	case <-time.After(time.Second):
		t.Fatal("SendScreen still blocked after stop")
	}
}

// A ritmo de LAN la ráfaga crece con la tasa: sale rápido (sin un timer por paquete) y sin pasar
// la tasa.
func TestUDP_ScreenLANRate(t *testing.T) {
	sender, receiver := listenPair(t)
	_ = receiver.SetReadBuffer(SocketBufferBytes)
	sender.SetScreenRate(ScreenLANRateBytes)
	targets := []*net.UDPAddr{receiver.LocalAddr().(*net.UDPAddr)}
	const packets = 600 // ~750 KB: una imagen completa de 1080p
	payload := make([]byte, 1200)
	start := time.Now()
	go func() {
		for i := 0; i < packets; i++ {
			sender.SendScreen(targets, Packet{Kind: PacketScreenTile, Payload: payload}, nil)
		}
	}()
	got, last := countArrivals(t, receiver, packets, 5*time.Second)
	if got != packets {
		t.Fatalf("received %d of %d", got, packets)
	}
	total := packets * sealedSize(len(payload))
	want := time.Duration(float64(total) / ScreenLANRateBytes * float64(time.Second))
	if elapsed := last.Sub(start); elapsed < want*8/10 || elapsed > want*4 {
		t.Fatalf("%d bytes at the LAN rate took %v, want about %v", total, elapsed, want)
	}
}

// El cursor sale sin cola: llega aunque la cola de pantalla esté llena.
func TestUDP_SendToSkipsTheQueue(t *testing.T) {
	sender, receiver := listenPair(t)
	sender.SetScreenRate(screenMinRate)
	dead := []*net.UDPAddr{{IP: net.IPv4(127, 0, 0, 1), Port: 9}}
	payload := make([]byte, 1200)
	for i := 0; i < screenQueue; i++ {
		sender.TryRelayScreen(Packet{Kind: PacketScreenTile, Payload: payload}, dead)
	}
	sender.SendTo([]*net.UDPAddr{receiver.LocalAddr().(*net.UDPAddr)}, Packet{Kind: PacketScreenCursor, Payload: []byte{1, 2, 3}})
	if got, _ := countArrivals(t, receiver, 1, time.Second); got != 1 {
		t.Fatal("SendTo waited behind the screen queue")
	}
}
