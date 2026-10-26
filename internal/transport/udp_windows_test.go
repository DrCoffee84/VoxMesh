//go:build windows

package transport

import "testing"

// Con SO_EXCLUSIVEADDRUSE una segunda instancia no puede ligarse al mismo puerto (con
// SO_REUSEADDR se ligaba y Windows repartía los paquetes entre las dos).
func TestListenPortIsExclusive(t *testing.T) {
	first, err := Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Listen(first.Address().String())
	if err == nil {
		second.Close()
		t.Fatal("el segundo Listen en el mismo puerto no falló")
	}
}
