package identity

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestLoadOrCreate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "VoxMesh")
	first, created, err := LoadOrCreate(dir)
	if err != nil || !created {
		t.Fatalf("la primera vez se crea: created=%v err=%v", created, err)
	}
	second, created, err := LoadOrCreate(dir)
	if err != nil || created {
		t.Fatalf("la segunda vez se carga: created=%v err=%v", created, err)
	}
	if first.Public() != second.Public() {
		t.Fatal("cargada no es la misma llave")
	}
	info, err := os.Stat(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 && os.PathSeparator == '/' {
		t.Errorf("permisos %v: solo el usuario", mode)
	}

	// Archivo roto → una nueva, guardada (la vieja no se puede leer).
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte("{basura"), 0o600); err != nil {
		t.Fatal(err)
	}
	third, created, err := LoadOrCreate(dir)
	if err != nil || !created || third.Public() == first.Public() {
		t.Fatalf("roto: created=%v err=%v", created, err)
	}
	again, err := Load(dir)
	if err != nil || again.Public() != third.Public() {
		t.Fatalf("la nueva quedó guardada: %v", err)
	}

	// Semilla del largo equivocado y otra versión: rotas.
	for _, broken := range []string{`{"version":1,"seed":"AAAA"}`, `{"version":2,"seed":"` + "x" + `"}`} {
		if err := os.WriteFile(filepath.Join(dir, FileName), []byte(broken), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(dir); err == nil {
			t.Errorf("%s se cargó", broken)
		}
	}
}

func TestSignVerify(t *testing.T) {
	a, b := New(), New()
	msg := []byte("número")
	sig := a.Sign("join", msg)
	if !Verify(a.Public(), "join", msg, sig) {
		t.Fatal("la firma propia no verifica")
	}
	if Verify(b.Public(), "join", msg, sig) {
		t.Error("verifica con la llave de otro")
	}
	if Verify(a.Public(), "hello", msg, sig) {
		t.Error("una firma de un uso sirve para otro")
	}
	if Verify(a.Public(), "join", []byte("otro"), sig) {
		t.Error("verifica otro mensaje")
	}
	if Verify(a.Public(), "join", msg, sig[:10]) || Verify(Key{}, "join", msg, sig) {
		t.Error("firma corta o llave vacía")
	}
}

func TestKeyText(t *testing.T) {
	id := New()
	text := id.Public().String()
	if len(text) != 43 {
		t.Errorf("largo %d", len(text))
	}
	back, err := ParseKey(text)
	if err != nil || back != id.Public() {
		t.Fatalf("ida y vuelta: %v", err)
	}
	for _, bad := range []string{"", "abc", text + "A", text[:42] + "!", Key{}.String()} {
		if _, err := ParseKey(bad); err == nil {
			t.Errorf("%q se leyó", bad)
		}
	}
	short := id.Public().Short()
	if !regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{4}-[0-9A-HJKMNP-TV-Z]{4}$`).MatchString(short) {
		t.Errorf("ID corto %q", short)
	}
	if short != id.Public().Short() || short == New().Public().Short() {
		t.Error("el ID corto es fijo por llave y distinto entre llaves")
	}
}
