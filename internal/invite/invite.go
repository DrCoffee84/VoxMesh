// Package invite arma y lee el código de invitación de una sala: la dirección del host y la clave
// de la sala, como texto para pasar por WhatsApp y pegar en "Conectar".
//
// Formato: "VM1-" + base64url sin relleno de [largo de la dirección (1 byte)][dirección "IP:puerto"
// en ASCII][clave de 32 bytes]. La dirección es siempre una IP literal (nunca DNS).
package invite

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/netip"
	"strings"
)

// Prefix identifica la versión del código.
const Prefix = "VM1-"

// KeySize es el largo de la clave de la sala.
const KeySize = 32

// Invite es lo que trae un código: a quién conectarse y con qué clave.
type Invite struct {
	Addr netip.AddrPort
	Key  [KeySize]byte
}

var (
	ErrNotInvite = errors.New("no es una invitación de VoxMesh")
	ErrVersion   = errors.New("invitación de otra versión de VoxMesh: actualizá la app")
	ErrEncoding  = errors.New("invitación incompleta o mal copiada")
	ErrAddress   = errors.New("la invitación no trae una dirección válida")
	ErrKey       = errors.New("la invitación no trae una clave válida")
)

// Format arma el código de invitación.
func Format(addr netip.AddrPort, key [KeySize]byte) string {
	text := addr.String()
	data := make([]byte, 0, 1+len(text)+KeySize)
	data = append(data, byte(len(text)))
	data = append(data, text...)
	data = append(data, key[:]...)
	return Prefix + base64.RawURLEncoding.EncodeToString(data)
}

// Parse lee un código de invitación (los espacios alrededor no importan).
func Parse(code string) (Invite, error) {
	code = strings.TrimSpace(code)
	body, ok := strings.CutPrefix(code, Prefix)
	if !ok {
		if isOtherVersion(code) {
			return Invite{}, ErrVersion
		}
		return Invite{}, ErrNotInvite
	}
	data, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil || len(data) < 1 {
		return Invite{}, ErrEncoding
	}
	addrLen := int(data[0])
	if len(data) < 1+addrLen {
		return Invite{}, ErrEncoding
	}
	addr, err := netip.ParseAddrPort(string(data[1 : 1+addrLen]))
	if err != nil || addr.Port() == 0 || !addr.Addr().IsValid() || addr.Addr().IsUnspecified() {
		return Invite{}, ErrAddress
	}
	key := data[1+addrLen:]
	if len(key) != KeySize {
		return Invite{}, fmt.Errorf("%w (%d bytes)", ErrKey, len(key))
	}
	result := Invite{Addr: addr}
	copy(result.Key[:], key)
	return result, nil
}

// isOtherVersion reconoce "VM<número>-…" de una versión que esta no conoce.
func isOtherVersion(code string) bool {
	rest, ok := strings.CutPrefix(code, "VM")
	if !ok {
		return false
	}
	digits := 0
	for digits < len(rest) && rest[digits] >= '0' && rest[digits] <= '9' {
		digits++
	}
	return digits > 0 && digits < len(rest) && rest[digits] == '-'
}
