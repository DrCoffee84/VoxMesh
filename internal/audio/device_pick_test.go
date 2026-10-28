package audio

import (
	"testing"

	"voxmesh/internal/config"
)

func TestPickDevice(t *testing.T) {
	list := []Device{
		{Name: "Auriculares (USB)", ID: "a1"},
		{Name: "Auriculares (USB)", ID: "b2"},
		{Name: "Altavoces (Realtek)", ID: "c3"},
	}
	cases := []struct {
		name        string
		want        Device
		wantIndex   int
		wantMissing bool
	}{
		{"por ID", Device{Name: "Altavoces (Realtek)", ID: "c3"}, 2, false},
		{"mismo nombre: gana el ID", Device{Name: "Auriculares (USB)", ID: "b2"}, 1, false},
		{"ID que ya no está: predeterminado aunque el nombre exista", Device{Name: "Auriculares (USB)", ID: "zz"}, -1, true},
		{"sin ID: predeterminado aunque el nombre exista", Device{Name: "Altavoces (Realtek)"}, -1, true},
		{"predeterminado", DefaultChoice, -1, false},
		{"vacío = predeterminado", Device{}, -1, false},
		{"nombre del predeterminado", Device{Name: config.DefaultDevice}, -1, false},
	}
	for _, c := range cases {
		index, missing := PickDevice(list, c.want)
		if index != c.wantIndex || missing != c.wantMissing {
			t.Errorf("%s: PickDevice = (%d, %v), quería (%d, %v)", c.name, index, missing, c.wantIndex, c.wantMissing)
		}
	}
}
