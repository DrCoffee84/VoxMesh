package presence

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"voxmesh/internal/history"
	"voxmesh/internal/identity"
	"voxmesh/internal/room"
)

var (
	testFrom  = ID{1, 2, 3, 4, 5, 6, 7, 8}
	testCall  = ID{9, 9, 9, 9, 9, 9, 9, 9}
	testKey   = identity.Key{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32}
	testNonce = [32]byte{7, 7, 7}
	testSig   = [64]byte{5, 4, 3, 2, 1}
)

func TestTextLimitsMatchTheRoom(t *testing.T) {
	if MaxNameRunes != room.MaxUsernameRunes {
		t.Errorf("MaxNameRunes = %d, la sala usa %d", MaxNameRunes, room.MaxUsernameRunes)
	}
	if MaxRoomRunes != history.MaxRoomNameRunes {
		t.Errorf("MaxRoomRunes = %d, la sala usa %d", MaxRoomRunes, history.MaxRoomNameRunes)
	}
}

func TestEncodeDecodeEveryKind(t *testing.T) {
	invite := "VM1-AA0xOTIuMTY4LjEuMzk6NDc4MzAAAQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHw"
	cases := []Message{
		{Kind: KindPresence, From: testFrom, Name: "Coffee"},
		{Kind: KindPresence, From: testFrom, Name: "Juan Pérez 🎮", InRoom: true},
		{Kind: KindPresence, From: testFrom, Name: "Budo", InRoom: true, Room: "Mi sala"},
		{Kind: KindBye, From: testFrom},
		{Kind: KindCall, From: testFrom, Call: testCall, Name: "Coffee", Room: "Los pibes"},
		{Kind: KindCall, From: testFrom, Call: testCall, Name: "Coffee"}, // sala vacía: un cliente que todavía no sabe el nombre
		{Kind: KindAccept, From: testFrom, Call: testCall},
		{Kind: KindDecline, From: testFrom, Call: testCall, Reason: ReasonRejected},
		{Kind: KindDecline, From: testFrom, Call: testCall, Reason: ReasonBusy},
		{Kind: KindDecline, From: testFrom, Call: testCall, Reason: ReasonInRoom},
		{Kind: KindHangup, From: testFrom, Call: testCall},
		{Kind: KindInvite, From: testFrom, Call: testCall, Invite: invite},
		{Kind: KindJoin, From: testFrom, Call: testCall, Name: "Juan", Key: testKey},
		{Kind: KindChallenge, From: testFrom, Call: testCall, Nonce: testNonce},
		{Kind: KindProof, From: testFrom, Call: testCall, Sig: testSig},
		{Kind: KindDecline, From: testFrom, Call: testCall, Reason: ReasonNoRoom},
		{Kind: KindDecline, From: testFrom, Call: testCall, Reason: ReasonNoAnswer},
	}
	for _, want := range cases {
		packet, err := Encode(want)
		if err != nil {
			t.Fatalf("Encode(%+v): %v", want, err)
		}
		if len(packet) > MaxPacket {
			t.Errorf("%+v: %d bytes", want, len(packet))
		}
		got, err := Decode(packet)
		if err != nil {
			t.Fatalf("Decode de %+v: %v", want, err)
		}
		if got != want {
			t.Errorf("ida y vuelta:\n got %+v\nwant %+v", got, want)
		}
	}
}

func TestEncodeRejects(t *testing.T) {
	cases := []struct {
		name string
		m    Message
	}{
		{"presencia sin nombre", Message{Kind: KindPresence, From: testFrom}},
		{"nombre de solo espacios y control", Message{Kind: KindPresence, From: testFrom, Name: " \t\x00 "}},
		{"llamada sin nombre", Message{Kind: KindCall, From: testFrom, Call: testCall}},
		{"motivo desconocido", Message{Kind: KindDecline, From: testFrom, Call: testCall, Reason: 9}},
		{"invitación vacía", Message{Kind: KindInvite, From: testFrom, Call: testCall}},
		{"invitación con espacios", Message{Kind: KindInvite, From: testFrom, Call: testCall, Invite: "VM1-a b"}},
		{"invitación larga", Message{Kind: KindInvite, From: testFrom, Call: testCall, Invite: strings.Repeat("a", MaxInviteBytes+1)}},
		{"tipo desconocido", Message{Kind: 42, From: testFrom}},
		{"pedido sin llave", Message{Kind: KindJoin, From: testFrom, Call: testCall, Name: "Juan"}},
		{"pedido sin nombre", Message{Kind: KindJoin, From: testFrom, Call: testCall, Key: testKey}},
	}
	for _, c := range cases {
		if _, err := Encode(c.m); err == nil {
			t.Errorf("%s: Encode tenía que fallar", c.name)
		}
	}
}

func TestDecodeRejectsJunk(t *testing.T) {
	good, _ := Encode(Message{Kind: KindCall, From: testFrom, Call: testCall, Name: "Coffee", Room: "Los pibes"})
	presence, _ := Encode(Message{Kind: KindPresence, From: testFrom, Name: "Coffee"})
	decline, _ := Encode(Message{Kind: KindDecline, From: testFrom, Call: testCall, Reason: ReasonBusy})
	invite, _ := Encode(Message{Kind: KindInvite, From: testFrom, Call: testCall, Invite: "VM1-abc"})
	join, _ := Encode(Message{Kind: KindJoin, From: testFrom, Call: testCall, Name: "Juan", Key: testKey})
	proof, _ := Encode(Message{Kind: KindProof, From: testFrom, Call: testCall, Sig: testSig})
	// Un aviso de "libre" que igual trae una sala.
	freeWithRoom := append(append(bytes.Clone(presence[:len(presence)-1]), 3), "abc"...)
	zeroKey := bytes.Clone(join)
	copy(zeroKey[len(zeroKey)-32:], make([]byte, 32))

	with := func(packet []byte, at int, value byte) []byte {
		out := bytes.Clone(packet)
		out[at] = value
		return out
	}
	// En la presencia: [0:4] magic, [4] tipo, [5:13] ID, [13] estado, [14] largo, [15:] nombre,
	// después el largo y la sala.
	nameAt := 15
	cases := []struct {
		name   string
		packet []byte
		want   error
	}{
		{"vacío", nil, ErrMagic},
		{"corto", []byte("VMP1"), ErrMagic},
		{"otro magic", with(good, 3, '2'), ErrMagic},
		{"paquete de la sala", append([]byte("VM02"), make([]byte, 40)...), ErrMagic},
		{"tipo desconocido", with(good, 4, 99), ErrKind},
		{"tipo cero", with(good, 4, 0), ErrKind},
		{"un byte de más", append(bytes.Clone(good), 0), ErrMalformed},
		{"cortado", good[:len(good)-3], ErrMalformed},
		{"sin el ID", good[:9], ErrMagic},
		{"estado raro", with(presence, 13, 2), ErrMalformed},
		{"largo del nombre de más", with(presence, 14, 200), ErrMalformed},
		{"nombre en UTF-8 roto", with(presence, nameAt, 0xff), ErrText},
		{"nombre vacío", append(with(presence, 14, 0)[:15], 0), ErrText},
		{"libre con sala", freeWithRoom, ErrMalformed},
		{"sin el largo de la sala", presence[:len(presence)-1], ErrMalformed},
		{"pedido con la llave en cero", zeroKey, ErrMalformed},
		{"pedido cortado", join[:len(join)-1], ErrMalformed},
		{"firma cortada", proof[:len(proof)-1], ErrMalformed},
		{"motivo raro", with(decline, len(decline)-1, 7), ErrMalformed},
		{"invitación con espacio", with(invite, len(invite)-1, ' '), ErrText},
		{"invitación no ASCII", with(invite, len(invite)-1, 0xc3), ErrText},
		{"demasiado grande", append(bytes.Clone(good), make([]byte, MaxPacket)...), ErrTooLarge},
	}
	for _, c := range cases {
		if _, err := Decode(c.packet); !errors.Is(err, c.want) {
			t.Errorf("%s: Decode = %v, quería %v", c.name, err, c.want)
		}
	}
}

func TestDecodeCleansText(t *testing.T) {
	// Un aviso armado a mano (sin pasar por Encode), como lo mandaría alguien con otra app.
	raw := func(name string) []byte {
		packet := append([]byte("VMP1"), byte(KindPresence))
		packet = append(packet, testFrom[:]...)
		packet = append(packet, 0, byte(len(name)))
		return append(append(packet, name...), 0) // sin sala
	}
	cases := []struct{ in, want string }{
		{"  Juan  ", "Juan"},
		{"Ju\nan\t", "Juan"},
		{"‮Juan", "Juan"}, // da vuelta el texto que sigue
		{"Juan⁦x", "Juanx"},
		{strings.Repeat("ñ", 40), strings.Repeat("ñ", MaxNameRunes)},
	}
	for _, c := range cases {
		m, err := Decode(raw(c.in))
		if err != nil {
			t.Fatalf("%q: %v", c.in, err)
		}
		if m.Name != c.want {
			t.Errorf("%q → %q, quería %q", c.in, m.Name, c.want)
		}
	}
	if _, err := Decode(raw("‮\n ")); !errors.Is(err, ErrText) {
		t.Errorf("un nombre que queda vacío: %v", err)
	}
}

func TestEncodeClampsLongTexts(t *testing.T) {
	long := Message{Kind: KindCall, From: testFrom, Call: testCall, Name: strings.Repeat("😀", 50), Room: strings.Repeat("😀", 100)}
	packet, err := Encode(long)
	if err != nil {
		t.Fatal(err)
	}
	if len(packet) > MaxPacket {
		t.Fatalf("%d bytes", len(packet))
	}
	got, err := Decode(packet)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != strings.Repeat("😀", MaxNameRunes) {
		t.Errorf("nombre: %d runas", len([]rune(got.Name)))
	}
	// 4 bytes por runa: entran 63 en los 255 bytes del campo, sin partir ninguna.
	if got.Room != strings.Repeat("😀", 63) {
		t.Errorf("sala: %d runas, %d bytes", len([]rune(got.Room)), len(got.Room))
	}
	// El aviso con el nombre y la sala al tope (F10) también entra.
	packet, err = Encode(Message{Kind: KindPresence, From: testFrom, Name: long.Name, InRoom: true, Room: long.Room})
	if err != nil || len(packet) > MaxPacket {
		t.Fatalf("aviso al tope: %d bytes, %v", len(packet), err)
	}
	got, err = Decode(packet)
	if err != nil || got.Room != strings.Repeat("😀", 63) || !got.InRoom {
		t.Errorf("aviso al tope: %+v %v", got, err)
	}
	// Libre: la sala no viaja aunque el estado la traiga.
	packet, _ = Encode(Message{Kind: KindPresence, From: testFrom, Name: "Juan", Room: "Mi sala"})
	if got, err := Decode(packet); err != nil || got.Room != "" {
		t.Errorf("libre con sala: %+v %v", got, err)
	}
}

func TestNewIDIsRandom(t *testing.T) {
	a, b := NewID(), NewID()
	if a == b || a.IsZero() || b.IsZero() {
		t.Errorf("IDs: %s %s", a, b)
	}
	if len(a.String()) != 16 {
		t.Errorf("String() = %q", a.String())
	}
}

// FuzzDecode: nada de la red hace caer a Decode, y lo que lee se vuelve a armar igual.
func FuzzDecode(f *testing.F) {
	for _, m := range []Message{
		{Kind: KindPresence, From: testFrom, Name: "Coffee", InRoom: true},
		{Kind: KindCall, From: testFrom, Call: testCall, Name: "Coffee", Room: "Los pibes"},
		{Kind: KindInvite, From: testFrom, Call: testCall, Invite: "VM1-abc"},
		{Kind: KindDecline, From: testFrom, Call: testCall, Reason: ReasonBusy},
		{Kind: KindPresence, From: testFrom, Name: "Budo", InRoom: true, Room: "Mi sala"},
		{Kind: KindJoin, From: testFrom, Call: testCall, Name: "Juan", Key: testKey},
		{Kind: KindChallenge, From: testFrom, Call: testCall, Nonce: testNonce},
		{Kind: KindProof, From: testFrom, Call: testCall, Sig: testSig},
	} {
		packet, _ := Encode(m)
		f.Add(packet)
	}
	f.Add([]byte("VMP1"))
	f.Fuzz(func(t *testing.T, data []byte) {
		m, err := Decode(data)
		if err != nil {
			return
		}
		again, err := Encode(m)
		if err != nil {
			t.Fatalf("Decode leyó %+v y Encode no lo arma: %v", m, err)
		}
		back, err := Decode(again)
		if err != nil || back != m {
			t.Fatalf("ida y vuelta: %+v → %+v (%v)", m, back, err)
		}
	})
}
