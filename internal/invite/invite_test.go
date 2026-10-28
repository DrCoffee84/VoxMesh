package invite

import (
	"encoding/base64"
	"errors"
	"net/netip"
	"strings"
	"testing"
)

func testKey() [KeySize]byte {
	var key [KeySize]byte
	for i := range key {
		key[i] = byte(200 + i)
	}
	return key
}

func TestFormatParseRoundTrip(t *testing.T) {
	for _, addr := range []string{"186.57.10.200:47825", "100.101.102.103:47825", "192.168.0.10:7", "[2001:db8::1]:47825"} {
		want := Invite{Addr: netip.MustParseAddrPort(addr), Key: testKey()}
		code := Format(want.Addr, want.Key)
		if !strings.HasPrefix(code, Prefix) || strings.ContainsAny(code, "+/= \n") {
			t.Fatalf("código %q: no es texto seguro para pegar", code)
		}
		got, err := Parse("  " + code + "\n")
		if err != nil {
			t.Fatalf("Parse(%q): %v", code, err)
		}
		if got != want {
			t.Fatalf("ida y vuelta de %s: %+v", addr, got)
		}
	}
}

// raw arma un código a mano, para probar entradas que Format nunca produce.
func raw(addr string, key []byte) string {
	data := append([]byte{byte(len(addr))}, addr...)
	return Prefix + base64.RawURLEncoding.EncodeToString(append(data, key...))
}

func TestParseRejectsInvalid(t *testing.T) {
	key := testKey()
	valid := Format(netip.MustParseAddrPort("186.57.10.200:47825"), key)
	cases := []struct {
		name string
		code string
		want error
	}{
		{"vacío", "", ErrNotInvite},
		{"solo IP:puerto", "186.57.10.200:47825", ErrNotInvite},
		{"prefijo mal escrito", "VMx-" + valid[len(Prefix):], ErrNotInvite},
		{"versión desconocida", "VM2-" + valid[len(Prefix):], ErrVersion},
		{"base64 roto", valid[:20] + "*" + valid[21:], ErrEncoding},
		{"cuerpo vacío", Prefix, ErrEncoding},
		{"truncado", valid[:len(valid)-10], ErrKey},
		{"truncado en la dirección", valid[:len(Prefix)+6], ErrEncoding},
		{"clave de 31 bytes", raw("186.57.10.200:47825", key[:31]), ErrKey},
		{"clave de 33 bytes", raw("186.57.10.200:47825", append(key[:], 0)), ErrKey},
		{"sin puerto", raw("186.57.10.200", key[:]), ErrAddress},
		{"puerto 0", raw("186.57.10.200:0", key[:]), ErrAddress},
		{"nombre en vez de IP", raw("example.com:47825", key[:]), ErrAddress},
		{"dirección sin especificar", raw("0.0.0.0:47825", key[:]), ErrAddress},
	}
	for _, tc := range cases {
		if _, err := Parse(tc.code); !errors.Is(err, tc.want) {
			t.Errorf("%s: Parse(%q) = %v, quería %v", tc.name, tc.code, err, tc.want)
		}
	}
}
