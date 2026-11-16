// Package presence es "En tu red" (AGENDA F9 y F10): cada VoxMesh abierto avisa a su LAN o VPN
// quién es y si está libre o en qué sala, se puede llamar a otro con un clic y pedir entrar a la
// sala de otro. No usa la clave de ninguna sala: el aviso solo lleva un ID al azar de la sesión, el
// nombre, el estado y el nombre de la sala, así que un desconocido de la misma red (una red pública
// de Radmin, el Wi-Fi de un bar) solo ve nombres. La invitación a la sala viaja recién cuando el
// llamado atiende o el de la sala deja entrar, y solo a ese.
//
// Sin cgo y sin UI: el paquete (este archivo), la lista de quién está (roster.go), los sockets
// (service.go), la llamada (calls.go) y el pedido para entrar (joins.go).
package presence

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"voxmesh/internal/identity"
)

// Port es el puerto UDP fijo donde todos escuchan los avisos. No se puede cambiar en Ajustes como
// el de la sala (47830) y el del celular (47831): todos tienen que escuchar el mismo.
const Port = 47832

// MaxPacket es el tamaño máximo de un paquete: el más grande (una llamada con el nombre y la sala
// al tope) ocupa 406 bytes; un aviso con el nombre y la sala al tope, 399. Lo más grande se
// descarta sin leerlo.
const MaxPacket = 512

// Topes de los textos: los mismos que los de la sala (room.MaxUsernameRunes,
// history.MaxRoomNameRunes). Los bytes acotan el campo en el paquete (un largo de un byte).
const (
	MaxNameRunes   = 32
	MaxRoomRunes   = 64
	MaxInviteBytes = 128
	maxNameBytes   = MaxNameRunes * utf8.UTFMax
	maxRoomBytes   = 255
)

// magic abre cada paquete: "VoxMesh Presencia", versión 1. Sin compatibilidad hacia atrás: otra
// versión cambia el magic y sus paquetes se descartan.
var magic = [4]byte{'V', 'M', 'P', '1'}

// Kind es el tipo de mensaje.
type Kind byte

const (
	KindPresence Kind = 1 // "soy X, estoy libre / en una sala" (broadcast cada 3 s)
	KindBye      Kind = 2 // "me voy": cerré la app o apagué el check (broadcast)
	KindCall     Kind = 3 // "te llamo" (nombre del que llama y de la sala), directo al llamado
	KindAccept   Kind = 4 // "atiendo", directo al que llama
	KindDecline  Kind = 5 // "rechazo" / "estoy con otra llamada" / "estoy en una sala" (Reason)
	KindHangup   Kind = 6 // el que llama cortó (o se venció): el cartel del llamado se cierra
	KindInvite   Kind = 7 // la invitación a la sala (VM1-…), solo después de "atiendo" o de dejar entrar
	// Pedir entrar a la sala de otro (F10, joins.go): "quiero entrar" + mi llave → un número al
	// azar → ese número firmado con mi privada. Lo demás reusa Decline, Hangup e Invite.
	KindJoin      Kind = 8  // "quiero entrar" (Call = ID del pedido, Name, Key), directo al de la sala
	KindChallenge Kind = 9  // el desafío: Nonce, de vuelta al que pide
	KindProof     Kind = 10 // la respuesta: Sig = firma del desafío (joinProofMessage)
)

// Reason es por qué no se atendió (KindDecline).
type Reason byte

const (
	ReasonRejected Reason = 1 // tocó Rechazar
	ReasonBusy     Reason = 2 // ya tiene otra llamada en pantalla
	ReasonInRoom   Reason = 3 // está en una sala
	ReasonNoRoom   Reason = 4 // (pedido para entrar) ya no está en una sala
	ReasonNoAnswer Reason = 5 // (pedido para entrar) nadie contestó a tiempo (JoinTimeout)
)

// ID identifica una sesión de la app (el que avisa) o una llamada. Al azar: un ID nuevo cada vez
// que se abre la app, así nada va a disco y un desconocido no te sigue de una sesión a otra.
type ID [8]byte

// NewID es un ID al azar (crypto/rand).
func NewID() ID {
	var id ID
	_, _ = rand.Read(id[:]) // nunca falla (Go 1.24+)
	return id
}

func (id ID) String() string { return hex.EncodeToString(id[:]) }

// IsZero dice si el ID no se asignó.
func (id ID) IsZero() bool { return id == ID{} }

// Message es un paquete leído o por armar. Cada tipo usa solo algunos campos:
//
//	KindPresence:  From, Name, InRoom, Room (vacía fuera de una sala)
//	KindBye:       From
//	KindCall:      From, Call, Name (el que llama), Room (puede ir vacía)
//	KindAccept:    From, Call
//	KindDecline:   From, Call, Reason
//	KindHangup:    From, Call
//	KindInvite:    From, Call, Invite
//	KindJoin:      From, Call (el pedido), Name (el que pide), Key (su llave pública)
//	KindChallenge: From, Call, Nonce
//	KindProof:     From, Call, Sig
type Message struct {
	Kind   Kind
	From   ID
	Call   ID
	Name   string
	Room   string
	InRoom bool
	Reason Reason
	Invite string
	Key    identity.Key
	Nonce  [32]byte
	Sig    [ed25519.SignatureSize]byte
}

// Errores de Decode (para el log de lo descartado).
var (
	ErrTooLarge  = errors.New("paquete demasiado grande")
	ErrMagic     = errors.New("no es un paquete de presencia de VoxMesh")
	ErrKind      = errors.New("tipo desconocido")
	ErrMalformed = errors.New("paquete mal armado")
	ErrText      = errors.New("texto inválido")
)

// Encode arma el paquete de m. Los textos pasan por el mismo recorte que al leerlos (cleanText),
// así lo que sale es lo que va a leer el otro.
func Encode(m Message) ([]byte, error) {
	out := make([]byte, 0, 64)
	out = append(out, magic[:]...)
	out = append(out, byte(m.Kind))
	out = append(out, m.From[:]...)
	switch m.Kind {
	case KindPresence:
		name := cleanText(m.Name, MaxNameRunes, maxNameBytes)
		if name == "" {
			return nil, ErrText
		}
		status := byte(0)
		if m.InRoom {
			status = 1
		}
		out = append(out, status)
		out = appendText(out, name)
		room := ""
		if m.InRoom {
			room = cleanText(m.Room, MaxRoomRunes, maxRoomBytes)
		}
		out = appendText(out, room)
	case KindBye:
	case KindCall:
		name := cleanText(m.Name, MaxNameRunes, maxNameBytes)
		if name == "" {
			return nil, ErrText
		}
		out = append(out, m.Call[:]...)
		out = appendText(out, name)
		out = appendText(out, cleanText(m.Room, MaxRoomRunes, maxRoomBytes))
	case KindAccept, KindHangup:
		out = append(out, m.Call[:]...)
	case KindDecline:
		if !validReason(m.Reason) {
			return nil, ErrMalformed
		}
		out = append(out, m.Call[:]...)
		out = append(out, byte(m.Reason))
	case KindInvite:
		if !validInvite(m.Invite) {
			return nil, ErrText
		}
		out = append(out, m.Call[:]...)
		out = appendText(out, m.Invite)
	case KindJoin:
		name := cleanText(m.Name, MaxNameRunes, maxNameBytes)
		if name == "" || m.Key.IsZero() {
			return nil, ErrText
		}
		out = append(out, m.Call[:]...)
		out = appendText(out, name)
		out = append(out, m.Key[:]...)
	case KindChallenge:
		out = append(out, m.Call[:]...)
		out = append(out, m.Nonce[:]...)
	case KindProof:
		out = append(out, m.Call[:]...)
		out = append(out, m.Sig[:]...)
	default:
		return nil, ErrKind
	}
	return out, nil
}

// Decode lee un paquete. Es estricto: el largo exacto (ni un byte de más), cada campo dentro de su
// tope, texto en UTF-8 válido y un nombre que no quede vacío después de sacarle los caracteres de
// control. Cualquier otra cosa es un error y el paquete se descarta.
func Decode(b []byte) (Message, error) {
	if len(b) > MaxPacket {
		return Message{}, ErrTooLarge
	}
	if len(b) < len(magic)+1+len(ID{}) || [4]byte(b[:4]) != magic {
		return Message{}, ErrMagic
	}
	r := reader{b: b[4:]}
	m := Message{Kind: Kind(r.u8())}
	r.id(&m.From)
	switch m.Kind {
	case KindPresence:
		switch r.u8() {
		case 0:
		case 1:
			m.InRoom = true
		default:
			return Message{}, ErrMalformed
		}
		m.Name = r.text(maxNameBytes)
		m.Room = r.text(maxRoomBytes)
		if !m.InRoom && m.Room != "" {
			return Message{}, ErrMalformed
		}
	case KindBye:
	case KindCall:
		r.id(&m.Call)
		m.Name = r.text(maxNameBytes)
		m.Room = r.text(maxRoomBytes)
	case KindAccept, KindHangup:
		r.id(&m.Call)
	case KindDecline:
		r.id(&m.Call)
		m.Reason = Reason(r.u8())
		if !r.bad && !validReason(m.Reason) {
			return Message{}, ErrMalformed
		}
	case KindInvite:
		r.id(&m.Call)
		m.Invite = r.text(MaxInviteBytes)
	case KindJoin:
		r.id(&m.Call)
		m.Name = r.text(maxNameBytes)
		r.bytes(m.Key[:])
		if !r.bad && m.Key.IsZero() {
			return Message{}, ErrMalformed
		}
	case KindChallenge:
		r.id(&m.Call)
		r.bytes(m.Nonce[:])
	case KindProof:
		r.id(&m.Call)
		r.bytes(m.Sig[:])
	default:
		return Message{}, ErrKind
	}
	if r.bad || len(r.b) != 0 {
		return Message{}, ErrMalformed
	}
	if r.badText {
		return Message{}, ErrText
	}
	switch m.Kind {
	case KindPresence, KindCall, KindJoin:
		m.Name = cleanText(m.Name, MaxNameRunes, maxNameBytes)
		m.Room = cleanText(m.Room, MaxRoomRunes, maxRoomBytes)
		if m.Name == "" {
			return Message{}, ErrText
		}
	case KindInvite:
		if !validInvite(m.Invite) {
			return Message{}, ErrText
		}
	}
	return m, nil
}

func validReason(r Reason) bool {
	return r >= ReasonRejected && r <= ReasonNoAnswer
}

// validInvite: la invitación es ASCII imprimible (VM1-<base64url>), sin espacios. Qué dice la
// valida invite.Parse del lado de la UI.
func validInvite(s string) bool {
	if s == "" || len(s) > MaxInviteBytes {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] <= ' ' || s[i] > '~' {
			return false
		}
	}
	return true
}

// appendText agrega [largo u8][texto]. El texto ya viene recortado (≤ 255 bytes).
func appendText(out []byte, s string) []byte {
	out = append(out, byte(len(s)))
	return append(out, s...)
}

// cleanText deja un texto de la red listo para mostrar: sin caracteres de control ni de dirección
// del texto (los que dan vuelta lo que sigue, U+202A…U+202E y U+2066…U+2069), sin espacios en los
// bordes y cortado en maxRunes runas y maxBytes bytes (sin partir una runa).
func cleanText(s string, maxRunes, maxBytes int) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsControl(r) || isBidiControl(r) || r == utf8.RuneError {
			continue
		}
		b.WriteRune(r)
	}
	text := strings.TrimSpace(b.String())
	runes := 0
	for i, r := range text {
		if runes == maxRunes || i+utf8.RuneLen(r) > maxBytes {
			return strings.TrimSpace(text[:i])
		}
		runes++
	}
	return text
}

func isBidiControl(r rune) bool {
	return (r >= 0x202A && r <= 0x202E) || (r >= 0x2066 && r <= 0x2069) || r == 0x200E || r == 0x200F
}

// reader lee campos de un paquete; un campo que no entra marca bad y lo siguiente da vacío.
type reader struct {
	b       []byte
	bad     bool
	badText bool
}

func (r *reader) u8() byte {
	if r.bad || len(r.b) < 1 {
		r.bad = true
		return 0
	}
	v := r.b[0]
	r.b = r.b[1:]
	return v
}

func (r *reader) id(dst *ID) {
	if r.bad || len(r.b) < len(dst) {
		r.bad = true
		return
	}
	copy(dst[:], r.b)
	r.b = r.b[len(dst):]
}

// bytes copia len(dst) bytes fijos (una llave, un número, una firma).
func (r *reader) bytes(dst []byte) {
	if r.bad || len(r.b) < len(dst) {
		r.bad = true
		return
	}
	copy(dst, r.b)
	r.b = r.b[len(dst):]
}

func (r *reader) text(maxBytes int) string {
	n := int(r.u8())
	if r.bad {
		return ""
	}
	if n > maxBytes || n > len(r.b) {
		r.bad = true
		return ""
	}
	s := string(r.b[:n])
	r.b = r.b[n:]
	if !utf8.ValidString(s) {
		r.badText = true
	}
	return s
}
