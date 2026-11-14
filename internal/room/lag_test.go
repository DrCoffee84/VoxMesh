package room

import (
	"errors"
	"net"
	"reflect"
	"testing"
)

func TestPeerLagsRoundTrip(t *testing.T) {
	address := &net.UDPAddr{IP: net.IPv4(1, 2, 3, 4), Port: 47830}
	lags := []PeerLag{
		{ID: ParticipantID("Pepe", address.String()), MS: 120},
		{ID: "00112233aabbccdd", MS: 0},
		{ID: "ffffffffffffffff", MS: MaxPeerLagMS},
	}
	payload, err := EncodePeerLags(lags)
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) != 1+3*10 {
		t.Fatalf("mensaje de %d bytes, quería 31", len(payload))
	}
	got, err := DecodePeerLags(payload)
	if err != nil || !reflect.DeepEqual(got, lags) {
		t.Fatalf("DecodePeerLags = %+v, %v; quería %+v", got, err, lags)
	}

	// Vacío: el host no tiene números frescos (el cliente vuelve a "… ms").
	empty, err := EncodePeerLags(nil)
	if err != nil || len(empty) != 1 || empty[0] != 0 {
		t.Fatalf("vacío = %v, %v", empty, err)
	}
	if got, err := DecodePeerLags(empty); err != nil || len(got) != 0 {
		t.Fatalf("decodificar vacío = %+v, %v", got, err)
	}

	// Fuera de rango se acota.
	clamped, _ := EncodePeerLags([]PeerLag{{ID: "00112233aabbccdd", MS: 100000}, {ID: "00112233aabbccde", MS: -5}})
	if got, _ := DecodePeerLags(clamped); got[0].MS != MaxPeerLagMS || got[1].MS != 0 {
		t.Fatalf("acotado = %+v", got)
	}
}

func TestPeerLagsRejectsMalformed(t *testing.T) {
	valid, _ := EncodePeerLags([]PeerLag{{ID: "00112233aabbccdd", MS: 50}})
	tooMany := make([]byte, 1+(MaxParticipants+1)*10)
	tooMany[0] = MaxParticipants + 1
	cases := map[string][]byte{
		"vacío":               nil,
		"corto":               valid[:len(valid)-1],
		"largo":               append(append([]byte(nil), valid...), 0),
		"cantidad que miente": {2, 0, 1, 2, 3, 4, 5, 6, 7, 0, 50},
		"demasiados":          tooMany,
	}
	for name, payload := range cases {
		if _, err := DecodePeerLags(payload); !errors.Is(err, ErrPeerLags) {
			t.Errorf("%s: err = %v, quería ErrPeerLags", name, err)
		}
	}
	for name, lags := range map[string][]PeerLag{
		"ID que no es hex": {{ID: "pepe", MS: 1}},
		"ID corto":         {{ID: "0011", MS: 1}},
		"demasiados":       make([]PeerLag, MaxParticipants+1),
	} {
		if _, err := EncodePeerLags(lags); !errors.Is(err, ErrPeerLags) {
			t.Errorf("Encode %s: err = %v, quería ErrPeerLags", name, err)
		}
	}
}
