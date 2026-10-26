//go:build cgo

package ui

import (
	"slices"
	"testing"

	"voxmesh/internal/audio"
	"voxmesh/internal/config"
)

func TestDeviceOptions(t *testing.T) {
	devices := []audio.Device{
		{Name: "Auriculares (USB)", ID: "a1"},
		{Name: "Auriculares (USB)", ID: "b2"},
		{Name: "Altavoces", ID: "c3"},
	}
	options, byLabel, selected := deviceOptions(devices, audio.Device{Name: "Auriculares (USB)", ID: "b2"}, phoneMicInput)
	want := []string{config.DefaultDevice, phoneMicInput, "Auriculares (USB)", "Auriculares (USB) (2)", "Altavoces"}
	if !slices.Equal(options, want) {
		t.Fatalf("opciones = %q, quería %q", options, want)
	}
	if selected != "Auriculares (USB) (2)" || byLabel[selected].ID != "b2" {
		t.Fatalf("elegido %q (%+v), quería el de ID b2", selected, byLabel[selected])
	}
	if deviceByLabel(byLabel, phoneMicInput).Name != phoneMicInput || deviceByLabel(byLabel, config.DefaultDevice) != audio.DefaultChoice {
		t.Fatal("las opciones fijas no son su propio dispositivo")
	}
	if deviceByLabel(byLabel, "no existe") != audio.DefaultChoice {
		t.Fatal("una etiqueta desconocida tiene que ser el predeterminado")
	}

	// El guardado ya no está: se muestra el predeterminado (sin tocar la config).
	if _, _, selected := deviceOptions(devices, audio.Device{Name: "Bluetooth", ID: "zz"}); selected != config.DefaultDevice {
		t.Fatalf("dispositivo ausente: elegido %q, quería el predeterminado", selected)
	}
	// El celular guardado se muestra como el celular.
	if _, _, selected := deviceOptions(devices, audio.Device{Name: phoneMicInput}, phoneMicInput); selected != phoneMicInput {
		t.Fatalf("celular: elegido %q", selected)
	}
	// Config vieja sin ID: por nombre (el primero que se llama así).
	if _, byLabel, selected := deviceOptions(devices, audio.Device{Name: "Auriculares (USB)"}); byLabel[selected].ID != "a1" {
		t.Fatalf("config vieja: elegido %q (%+v)", selected, byLabel[selected])
	}
}
