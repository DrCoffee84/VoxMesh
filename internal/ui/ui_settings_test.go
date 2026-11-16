//go:build cgo

package ui

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"voxmesh/internal/audio"
	"voxmesh/internal/config"
	"voxmesh/internal/i18n"
)

// TestSettingsTabs: Ajustes en cinco pestañas (C33), cada control en la suya; al volver a entrar
// abre la última pestaña mirada; Guardar desde otra pestaña guarda lo cambiado en Red; con las dos
// "Configuración avanzada" abiertas entra en una ventana de ~960 px (C4). En los dos idiomas.
func TestSettingsTabs(t *testing.T) {
	for _, lang := range []string{"en", "es"} {
		t.Run(lang, func(t *testing.T) { testSettingsTabs(t, lang) })
	}
}

func testSettingsTabs(t *testing.T, lang string) {
	test.NewTempApp(t).Settings().SetTheme(voxTheme{})
	a := New(filepath.Join(t.TempDir(), "voxmesh.exe"))
	setLanguage(t, lang) // después de New, que aplica el de la config
	a.updateConfig(func(c *config.Config) { c.Username = "Pepe" })
	window := test.NewTempWindow(t, a.content())
	a.window = window
	window.Resize(fyne.NewSize(960, 700))

	tabs := openTestSettings(t, a)
	if len(tabs.Items) != len(settingsTabKeys) {
		t.Fatalf("%d pestañas, quería %d", len(tabs.Items), len(settingsTabKeys))
	}
	for i, key := range settingsTabKeys {
		if got := tabs.Items[i].Text; got != i18n.T(key) {
			t.Errorf("pestaña %d: %q, quería %q", i, got, i18n.T(key))
		}
	}
	if got := tabs.SelectedIndex(); got != 0 {
		t.Fatalf("la primera vez abre la pestaña %d, quería Entrada y salida", got)
	}

	// Cada control está en su pestaña y en ninguna otra.
	for _, c := range []struct {
		name  string
		tab   int
		match func(fyne.CanvasObject) bool
	}{
		{"fuente de voz", 0, labelWithText(i18n.T("settings.input_source"))},
		{"dispositivo de salida", 0, labelWithText(i18n.T("settings.output_device"))},
		{"QR del celular", 1, buttonWithText(i18n.T("settings.phone_show_qr"))},
		{"puerto de la sala", 2, entryWithText("47830")},
		{"LAN/VPN al invitar", 2, checkWithText(paramLabel(config.ParamByKey("show_lan_invites")))},
		{"aparecer en mi red", 2, checkWithText(paramLabel(config.ParamByKey("lan_presence_enabled")))},
		{"nombre", 3, entryWithText("Pepe")},
		{"idioma", 3, labelWithText(languageLabel)},
		{"X a la bandeja", 3, checkWithText(paramLabel(config.ParamByKey("close_to_tray")))},
		{"nivel de log", 4, labelWithText(i18n.T("settings.log_level"))},
		{"Acerca de", 4, buttonWithText(i18n.T("about.title"))},
	} {
		for i, item := range tabs.Items {
			if found := slices.ContainsFunc(settingsObjects(item.Content, true), c.match); found != (i == c.tab) {
				t.Errorf("%s: en la pestaña %q = %v, quería que esté solo en %q", c.name, item.Text, found, tabs.Items[c.tab].Text)
			}
		}
	}

	// C4: la pestaña más ancha (Entrada y salida, con las dos "Configuración avanzada" abiertas)
	// no agranda la ventana.
	advanced := 0
	for _, object := range settingsObjects(tabs.Items[0].Content, true) {
		if button, ok := object.(*widget.Button); ok && button.Text == i18n.T("settings.advanced") {
			test.Tap(button)
			advanced++
		}
	}
	if advanced != 2 {
		t.Fatalf("%d botones de Configuración avanzada en Entrada y salida, quería 2", advanced)
	}
	if got := window.Content().MinSize().Width; got > 960 {
		t.Errorf("con Configuración avanzada abierta Ajustes pide %v de ancho, quería a lo sumo 960", got)
	}

	// Volver y entrar de nuevo: la última pestaña mirada.
	tabs.SelectIndex(2)
	test.Tap(footerButton(t, window, i18n.T("settings.back")))
	if window.Content() != a.mainView {
		t.Fatal("Volver sin cambios no cerró Ajustes")
	}
	tabs = openTestSettings(t, a)
	if got := tabs.SelectedIndex(); got != 2 {
		t.Fatalf("al volver a entrar abre la pestaña %d, quería la 2 (Red)", got)
	}

	// Guardar mirando Sistema guarda el puerto cambiado en Red (en la config y en el disco).
	tabs.SelectIndex(4)
	for _, object := range settingsObjects(tabs.Items[2].Content, true) {
		if entry, ok := object.(*widget.Entry); ok && entry.Text == "47830" {
			entry.SetText("47999")
		}
	}
	test.Tap(footerButton(t, window, i18n.T("common.save")))
	if got := a.conf().ListenAddress; got != ":47999" {
		t.Fatalf("puerto de la sala después de Guardar: %q, quería \":47999\"", got)
	}
	if window.Content() != a.mainView {
		t.Fatal("Guardar no cerró Ajustes")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		saved, err := config.Load(a.cfgPath)
		if err == nil && saved.ListenAddress == ":47999" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("el puerto no llegó al disco: %q (%v)", saved.ListenAddress, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := openTestSettings(t, a).SelectedIndex(); got != 4 {
		t.Fatalf("después de Guardar abre la pestaña %d, quería la 4 (Sistema)", got)
	}
}

// openTestSettings abre Ajustes como el engranaje (sin listar dispositivos) y devuelve sus
// pestañas. La entrada es el celular, así el medidor no abre el micrófono de la PC.
func openTestSettings(t *testing.T, a *App) *container.AppTabs {
	t.Helper()
	a.updateConfig(func(c *config.Config) { c.InputDevice, c.InputDeviceID = phoneMicInput, "" })
	a.renderSettingsView(audio.DeviceLists{}, profileState{}, profileState{}, nil)
	for _, object := range settingsObjects(a.window.Content(), false) {
		if tabs, ok := object.(*container.AppTabs); ok {
			return tabs
		}
	}
	t.Fatal("Ajustes no tiene pestañas")
	return nil
}

// settingsObjects recorre contenedores y scrolls (sin entrar en los demás widgets). Con
// intoTabs también entra en las pestañas; sin él, solo lo que está afuera (Volver, Guardar…).
func settingsObjects(o fyne.CanvasObject, intoTabs bool) []fyne.CanvasObject {
	objects := []fyne.CanvasObject{o}
	switch o := o.(type) {
	case *fyne.Container:
		for _, child := range o.Objects {
			objects = append(objects, settingsObjects(child, intoTabs)...)
		}
	case *container.Scroll:
		objects = append(objects, settingsObjects(o.Content, intoTabs)...)
	case *container.AppTabs:
		if intoTabs {
			for _, item := range o.Items {
				objects = append(objects, settingsObjects(item.Content, intoTabs)...)
			}
		}
	}
	return objects
}

// footerButton es un botón de Ajustes que está afuera de las pestañas.
func footerButton(t *testing.T, window fyne.Window, text string) *widget.Button {
	t.Helper()
	for _, object := range settingsObjects(window.Content(), false) {
		if button, ok := object.(*widget.Button); ok && button.Text == text {
			return button
		}
	}
	t.Fatalf("no hay un botón %q afuera de las pestañas", text)
	return nil
}

func labelWithText(text string) func(fyne.CanvasObject) bool {
	return func(o fyne.CanvasObject) bool { l, ok := o.(*widget.Label); return ok && l.Text == text }
}

func buttonWithText(text string) func(fyne.CanvasObject) bool {
	return func(o fyne.CanvasObject) bool { b, ok := o.(*widget.Button); return ok && b.Text == text }
}

func entryWithText(text string) func(fyne.CanvasObject) bool {
	return func(o fyne.CanvasObject) bool { e, ok := o.(*widget.Entry); return ok && e.Text == text }
}

func checkWithText(text string) func(fyne.CanvasObject) bool {
	return func(o fyne.CanvasObject) bool { c, ok := o.(*widget.Check); return ok && c.Text == text }
}

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
