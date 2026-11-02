package phonemic

import (
	"crypto/rand"
	"encoding/hex"
	"net"
	"testing"
	"time"
)

func TestPhoneMicUDPSecurity(t *testing.T) {
	tokenBytes := make([]byte, 16)
	_, _ = rand.Read(tokenBytes)
	token := hex.EncodeToString(tokenBytes)

	pcmReceived := make(chan []byte, 10)
	server, err := Start(token, 0, Buffer{MS: 40, DriftSlackFrames: 2, DriftForceFrames: 4}, "Tester", func(pcm []byte, _ time.Time) {
		pcmReceived <- pcm
	}, nil)
	if err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer server.Stop()

	actualPort := server.Port()
	conn, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: actualPort})
	if err != nil {
		t.Fatalf("failed to dial udp: %v", err)
	}
	defer conn.Close()

	// 1. Send invalid packet (junk)
	_, _ = conn.Write([]byte("JUNK_PACKET_NOT_VMIC"))
	select {
	case <-pcmReceived:
		t.Fatal("junk packet should have been discarded")
	case <-time.After(100 * time.Millisecond):
	}

	// 2. Send VMIC_DISCOVER and verify response does NOT contain token
	_, _ = conn.Write([]byte("VMIC_DISCOVER"))
	_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	buf := make([]byte, 256)
	n, _, err := conn.ReadFrom(buf)
	if err == nil {
		resp := string(buf[:n])
		if resp != "VMIC_OFFER:Tester" {
			t.Fatalf("expected 'VMIC_OFFER:Tester', got %q", resp)
		}
	}

	// 3. Send packet with wrong token
	wrongToken := "wrongtoken1234567890"
	wrongHeader := []byte("VMIC")
	wrongHeader = append(wrongHeader, byte(len(wrongToken)))
	wrongHeader = append(wrongHeader, []byte(wrongToken)...)
	wrongPacket := append(wrongHeader, make([]byte, frameBytes)...)
	_, _ = conn.Write(wrongPacket)
	select {
	case <-pcmReceived:
		t.Fatal("packet with wrong token should have been discarded")
	case <-time.After(100 * time.Millisecond):
	}

	// 4. Send packet with truncated payload
	validHeader := []byte("VMIC")
	validHeader = append(validHeader, byte(len(token)))
	validHeader = append(validHeader, []byte(token)...)
	truncatedPacket := append(validHeader, make([]byte, frameBytes-10)...)
	_, _ = conn.Write(truncatedPacket)
	select {
	case <-pcmReceived:
		t.Fatal("truncated packet should have been discarded")
	case <-time.After(100 * time.Millisecond):
	}

	// 5. Send legitimate packet
	validPacket := append(validHeader, make([]byte, frameBytes)...)
	validPacket[len(validHeader)] = 42 // distinguish payload
	_, _ = conn.Write(validPacket)
	select {
	case pcm := <-pcmReceived:
		if len(pcm) != frameBytes || pcm[0] != 42 {
			t.Fatalf("unexpected pcm payload: len=%d, first byte=%d", len(pcm), pcm[0])
		}
	case <-time.After(1 * time.Second):
		t.Fatal("valid packet timed out and was not processed")
	}
}

// Con el buffer activado, si el que procesa los frames se traba, el reparto se pone al día: ningún
// frame se pierde ni sale con un hueco en su horario (con el ticker de antes Go salteaba los ticks
// atrasados y la voz salía más corta).
func TestForwardPCMCatchesUpAfterStall(t *testing.T) {
	tokenBytes := make([]byte, 16)
	_, _ = rand.Read(tokenBytes)
	token := hex.EncodeToString(tokenBytes)

	const frames = 30
	type delivery struct {
		first byte
		at    time.Time
	}
	delivered := make(chan delivery, frames)
	server, err := Start(token, 0, Buffer{Enabled: true, MS: 100, DriftSlackFrames: 10, DriftForceFrames: 10}, "Tester", func(pcm []byte, at time.Time) {
		if pcm[1] == 5 {
			time.Sleep(120 * time.Millisecond) // el procesamiento se traba 6 frames
		}
		delivered <- delivery{first: pcm[1], at: at}
	}, nil)
	if err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer server.Stop()

	conn, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: server.Port()})
	if err != nil {
		t.Fatalf("failed to dial udp: %v", err)
	}
	defer conn.Close()
	header := append([]byte("VMIC"), byte(len(token)))
	header = append(header, []byte(token)...)
	start := time.Now()
	for n := 0; n < frames; n++ {
		packet := append(append([]byte(nil), header...), make([]byte, frameBytes)...)
		for i := len(header); i+1 < len(packet); i += 2 {
			packet[i+1] = 0x30 // con voz: la deriva no lo tira por silencioso
		}
		packet[len(header)+1] = byte(n) // número de frame en la muestra 0
		if _, err := conn.Write(packet); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Until(start.Add(time.Duration(n+1) * frameDuration)))
	}

	var got []delivery
	timeout := time.After(2 * time.Second)
	for len(got) < frames {
		select {
		case d := <-delivered:
			got = append(got, d)
		case <-timeout:
			t.Fatalf("delivered %d of %d frames", len(got), frames)
		}
	}
	for i := 1; i < len(got); i++ {
		if got[i].first != got[i-1].first+1 {
			t.Fatalf("frame %d delivered after %d: a frame was lost", got[i].first, got[i-1].first)
		}
		if step := got[i].at.Sub(got[i-1].at); step != frameDuration {
			t.Fatalf("frame %d is %v after the previous one in the schedule, want %v", got[i].first, step, frameDuration)
		}
	}
}

func TestShouldDropForDrift(t *testing.T) {
	silent := make([]byte, frameBytes)
	loud := make([]byte, frameBytes)
	for i := 0; i+1 < len(loud); i += 2 {
		loud[i+1] = 0x30 // ~12288
	}
	cases := []struct {
		queued int
		frame  []byte
		want   bool
	}{
		{queued: 2, frame: silent, want: false},          // justo el prebuffer
		{queued: 4, frame: silent, want: false},          // dentro del margen
		{queued: 5, frame: silent, want: true},           // sobra: se tira el silencioso
		{queued: 5, frame: loud, want: false},            // sobra, pero con voz se espera
		{queued: 2 + 2 + 4 + 1, frame: loud, want: true}, // sobra mucho: se tira igual
	}
	for _, c := range cases {
		if got := shouldDropForDrift(c.queued, 2, 2, 4, c.frame); got != c.want {
			t.Fatalf("queued=%d loud=%v: got %v, want %v", c.queued, c.frame[1] != 0, got, c.want)
		}
	}
}
