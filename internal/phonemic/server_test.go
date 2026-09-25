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
	server, err := Start(token, 0, false, 40, "Tester", func(pcm []byte) {
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
