//go:build cgo

package ui

import (
	"slices"
	"testing"

	"voxmesh/internal/audio"
	"voxmesh/internal/config"
	"voxmesh/internal/i18n"
)

// setLanguage fija el idioma de la app para un test y lo restaura al terminar.
func setLanguage(t *testing.T, code string) {
	t.Helper()
	prev := i18n.Current()
	i18n.Set(code)
	t.Cleanup(func() { i18n.Set(string(prev)) })
}

// En español las etiquetas fijas son los mismos nombres que se guardan.
func TestDeviceOptions(t *testing.T) {
	setLanguage(t, "es")
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
	// Sin ID solo valen las opciones fijas: un nombre suelto se muestra como el predeterminado.
	if _, _, selected := deviceOptions(devices, audio.Device{Name: "Auriculares (USB)"}); selected != config.DefaultDevice {
		t.Fatalf("sin ID: elegido %q, quería el predeterminado", selected)
	}
}

// En inglés se muestran traducidas, pero detrás siguen los valores que se guardan en español.
func TestDeviceOptionsTranslatedLabels(t *testing.T) {
	setLanguage(t, "en")
	devices := []audio.Device{{Name: "Altavoces", ID: "c3"}}
	options, byLabel, selected := deviceOptions(devices, audio.Device{Name: phoneMicInput}, phoneMicInput)
	if want := []string{"System default", "Phone microphone", "Altavoces"}; !slices.Equal(options, want) {
		t.Fatalf("opciones = %q, quería %q", options, want)
	}
	if selected != "Phone microphone" || byLabel[selected].Name != phoneMicInput {
		t.Fatalf("celular: elegido %q (%+v)", selected, byLabel[selected])
	}
	if deviceByLabel(byLabel, "System default") != audio.DefaultChoice || audio.DefaultChoice.Name != config.DefaultDevice {
		t.Fatal(`"System default" tiene que ser el predeterminado, guardado como config.DefaultDevice`)
	}
	if _, _, selected := deviceOptions(devices, audio.Device{Name: config.DefaultDevice}); selected != "System default" {
		t.Fatalf("predeterminado guardado: elegido %q", selected)
	}
}
