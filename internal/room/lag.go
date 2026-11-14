package room

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
)

// PeerLag es el lag que el host midió con un participante (Ping/Pong, ida y vuelta). El host se
// lo manda a los clientes cada 2 s (transport.PacketPeerLag, AGENDA C18): el cliente solo mide al
// host, y así el chip de los demás muestra un número en vez de "… ms". Solo el número de ahora;
// el cliente no lo guarda en su historial.
type PeerLag struct {
	ID string // ID de participante (16 hex, ParticipantID)
	MS int    // ms, de 0 a MaxPeerLagMS
}

// MaxPeerLagMS es el lag más grande que entra en el mensaje (u16); uno mayor viaja como este.
const MaxPeerLagMS = 0xFFFF

// peerLagEntry es lo que ocupa cada participante: el ID en crudo (8 bytes, como en el audio) + ms.
const peerLagEntry = 8 + 2

// ErrPeerLags es un mensaje de lag mal formado: largo que no cierra, demasiados participantes o un
// ID que no es de 16 hex.
var ErrPeerLags = errors.New("mensaje de lag inválido")

// EncodePeerLags arma el mensaje: [cantidad u8] y por cada uno [ID 8 bytes][ms u16 BE]. A lo sumo
// MaxParticipants; un ms fuera de rango se acota a 0..MaxPeerLagMS.
func EncodePeerLags(lags []PeerLag) ([]byte, error) {
	if len(lags) > MaxParticipants {
		return nil, ErrPeerLags
	}
	payload := make([]byte, 1, 1+len(lags)*peerLagEntry)
	payload[0] = byte(len(lags))
	for _, lag := range lags {
		raw, err := hex.DecodeString(lag.ID)
		if err != nil || len(raw) != 8 {
			return nil, ErrPeerLags
		}
		payload = append(payload, raw...)
		payload = binary.BigEndian.AppendUint16(payload, uint16(min(max(lag.MS, 0), MaxPeerLagMS)))
	}
	return payload, nil
}

// DecodePeerLags lee el mensaje de EncodePeerLags. El largo tiene que ser exacto y la cantidad, a lo
// sumo MaxParticipants: nada que venga del paquete decide cuánto se reserva.
func DecodePeerLags(payload []byte) ([]PeerLag, error) {
	if len(payload) < 1 {
		return nil, ErrPeerLags
	}
	count := int(payload[0])
	if count > MaxParticipants || len(payload) != 1+count*peerLagEntry {
		return nil, ErrPeerLags
	}
	lags := make([]PeerLag, 0, count)
	for offset := 1; offset < len(payload); offset += peerLagEntry {
		lags = append(lags, PeerLag{
			ID: hex.EncodeToString(payload[offset : offset+8]),
			MS: int(binary.BigEndian.Uint16(payload[offset+8 : offset+peerLagEntry])),
		})
	}
	return lags, nil
}
