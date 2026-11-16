package room

import (
	"fmt"
	"testing"
	"time"

	"voxmesh/internal/identity"
)

func TestHelloSignature(t *testing.T) {
	me, other := identity.New(), identity.New()
	key := [32]byte{1, 2, 3}
	p := Participant{ID: "abc", Key: me.Public().String()}
	sig := SignHello(me, key, "abc")
	if !VerifyHello(p, key, sig) {
		t.Fatal("la firma propia no verifica")
	}
	// Con la llave de otro (sin su privada) no se anota nadie.
	if VerifyHello(Participant{ID: "abc", Key: other.Public().String()}, key, sig) {
		t.Error("un Hello con una llave ajena pasó")
	}
	if VerifyHello(Participant{ID: "abc", Key: other.Public().String()}, key, SignHello(me, key, "abc")) {
		t.Error("firmado por otra llave pasó")
	}
	// La firma es de esta sesión y de este participante.
	if VerifyHello(p, [32]byte{9}, sig) || VerifyHello(Participant{ID: "otro", Key: p.Key}, key, sig) {
		t.Error("la firma sirve en otra sala u otro participante")
	}
	if VerifyHello(Participant{ID: "abc"}, key, sig) || VerifyHello(p, key, nil) {
		t.Error("sin llave o sin firma pasó")
	}
}

func TestCleanTrusted(t *testing.T) {
	a, b := identity.New().Public().String(), identity.New().Public().String()
	now := time.Date(2026, 11, 16, 20, 0, 0, 0, time.UTC)
	list := CleanTrusted([]Trusted{
		{Key: a, Name: "Juan", By: "Coffee", At: now},
		{Key: a, Name: "Juancito", By: "Budo", At: now.Add(time.Hour)}, // la más reciente gana
		{Key: a, Name: "Viejo", At: now.Add(-time.Hour)},
		{Key: "basura", Name: "x", At: now},
		{Key: b, Name: "  Un nombre larguísimo que pasa de los treinta y dos  ", At: now},
	})
	if len(list) != 2 {
		t.Fatalf("%d entradas: %+v", len(list), list)
	}
	for _, entry := range list {
		switch entry.Key {
		case a:
			if entry.Name != "Juancito" || entry.By != "Budo" {
				t.Errorf("a: %+v", entry)
			}
		case b:
			if len([]rune(entry.Name)) > MaxUsernameRunes {
				t.Errorf("nombre sin recortar: %q", entry.Name)
			}
		}
	}

	// Tope: quedan las más recientes.
	var many []Trusted
	for i := range MaxTrusted + 5 {
		many = append(many, Trusted{Key: identity.New().Public().String(), Name: fmt.Sprint(i), At: now.Add(time.Duration(i) * time.Minute)})
	}
	clean := CleanTrusted(many)
	if len(clean) != MaxTrusted {
		t.Fatalf("tope: %d", len(clean))
	}
	for _, entry := range clean {
		if entry.Name < "5" && len(entry.Name) == 1 {
			t.Errorf("quedó una de las más viejas: %q", entry.Name)
		}
	}

	merged := MergeTrusted(list, []Trusted{{Key: b, Name: "Nuevo", At: now.Add(2 * time.Hour)}})
	if !TrustedHas(merged, mustKey(t, b)) || len(merged) != 2 {
		t.Errorf("merge: %+v", merged)
	}
	if SameTrusted(list, merged) || !SameTrusted(merged, MergeTrusted(merged, nil)) {
		t.Error("SameTrusted")
	}
	missing := MissingTrusted(merged, list)
	if len(missing) != 1 || missing[0].Name != "Nuevo" {
		t.Errorf("faltan: %+v", missing)
	}

	data, err := EncodeTrustFile(merged)
	if err != nil {
		t.Fatal(err)
	}
	if back := DecodeTrustFile(data); !SameTrusted(back, merged) {
		t.Errorf("archivo ida y vuelta: %+v", back)
	}
	if DecodeTrustFile([]byte("{roto")) != nil || DecodeTrustFile([]byte(`{"version":9,"trusted":[]}`)) != nil {
		t.Error("archivo roto u otra versión: vacío")
	}
}

func mustKey(t *testing.T, s string) identity.Key {
	t.Helper()
	key, err := identity.ParseKey(s)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// Un estado lleno con la lista de confianza al tope sigue entrando holgado en un datagrama.
func TestFullStateWithTrustedFits(t *testing.T) {
	state := State{Name: "Una sala con un nombre bastante largo para probar", Epoch: 3, HostID: "p0"}
	for i := range MaxParticipants {
		state.Participants = append(state.Participants, Participant{
			ID: fmt.Sprintf("p%d-0123456789", i), Username: "Nombre de treinta y dos letras!!", Address: "255.255.255.255:65535",
			Order: i, Connected: true, CanBeHost: true, LastSeenUTC: time.Now(), Key: identity.New().Public().String(),
		})
	}
	state.Participants[0].ID = "p0"
	for range MaxTrusted {
		state.Trusted = append(state.Trusted, Trusted{Key: identity.New().Public().String(), Name: "Nombre de treinta y dos letras!!", By: "Nombre de treinta y dos letras!!", At: time.Now()})
	}
	data, err := Encode(Envelope{Kind: StateMessage, Room: &state})
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > 16*1024 {
		t.Errorf("estado lleno: %d bytes", len(data))
	}
}
