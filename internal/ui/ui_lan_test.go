//go:build cgo

package ui

import (
	"image/png"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"voxmesh/internal/config"
	"voxmesh/internal/i18n"
	"voxmesh/internal/presence"
	"voxmesh/internal/transport"
)

// saveShot guarda una captura de window en $VOXMESH_SHOTS/<name>.png, solo si esa variable está
// puesta: para mirar cómo quedó una pantalla sin abrir la app.
func saveShot(t *testing.T, window fyne.Window, name string) {
	t.Helper()
	dir := os.Getenv("VOXMESH_SHOTS")
	if dir == "" {
		return
	}
	relayout(window)
	file, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := png.Encode(file, window.Canvas().Capture()); err != nil {
		t.Fatal(err)
	}
}

// relayout vuelve a acomodar la ventana como la app en cada cuadro (EnsureMinSize del loop de
// glfw); el canvas de test no lo hace y algo que cambió de alto quedaba con el tamaño viejo. Cambia
// el alto (la columna izquierda tiene ancho fijo, y un Resize al mismo tamaño no hace nada).
func relayout(window fyne.Window) {
	size := window.Canvas().Size()
	window.Resize(size.AddWidthHeight(0, 1))
	window.Resize(size)
}

// fitsDefaultWindow: la ventana de fábrica (620 px de alto) entra con la tarjeta como esté.
func fitsDefaultWindow(t *testing.T, window fyne.Window, what string) {
	t.Helper()
	if height := window.Content().MinSize().Height; height > config.DefaultWindowHeight {
		t.Errorf("%s: la ventana necesita %v de alto, la de fábrica tiene %v", what, height, config.DefaultWindowHeight)
	}
}

// labelsIn son los labels de object, en orden.
func labelsIn(object fyne.CanvasObject) []*widget.Label {
	switch o := object.(type) {
	case *widget.Label:
		return []*widget.Label{o}
	case *fyne.Container:
		var labels []*widget.Label
		for _, child := range o.Objects {
			labels = append(labels, labelsIn(child)...)
		}
		return labels
	}
	return nil
}

// TestLANCard: la tarjeta "En tu red" (F9) dice quién está, con su estado, sin ensanchar la
// columna ni hacer crecer la ventana de fábrica; con la ventana alta muestra hasta tres renglones
// enteros y con más hace scroll; sin nadie, apagada o con el puerto ocupado, un texto dice por qué;
// ☰ la oculta con Host y Cliente. En los dos idiomas.
func TestLANCard(t *testing.T) {
	for _, lang := range []string{"es", "en"} {
		t.Run(lang, func(t *testing.T) { testLANCard(t, lang) })
	}
}

func testLANCard(t *testing.T, lang string) {
	test.NewTempApp(t).Settings().SetTheme(voxTheme{})
	a := New(filepath.Join(t.TempDir(), "voxmesh.exe"))
	setLanguage(t, lang)
	window := test.NewTempWindow(t, a.content())
	a.window = window
	window.Resize(fyne.NewSize(config.DefaultWindowWidth, config.DefaultWindowHeight))

	if !a.lanHint.Visible() || a.lanHint.Text != i18n.T("lan.empty") || a.lanScroll.Visible() {
		t.Errorf("sin nadie: hint %q visible=%v, lista visible=%v", a.lanHint.Text, a.lanHint.Visible(), a.lanScroll.Visible())
	}
	relayout(window)
	fitsDefaultWindow(t, window, "sin nadie")
	saveShot(t, window, "lan_card_empty_"+lang)

	long := strings.Repeat("Nombre larguísimo ", 3)
	a.showLANPeers(lanOn, []presence.Peer{
		{ID: presence.ID{1}, Name: "Juan"},
		{ID: presence.ID{2}, Name: "Pedro", InRoom: true},
		{ID: presence.ID{3}, Name: long},
	})
	if a.lanHint.Visible() || !a.lanScroll.Visible() || len(a.lanList.Objects) != 3 {
		t.Fatalf("con 3: hint visible=%v, %d renglones", a.lanHint.Visible(), len(a.lanList.Objects))
	}
	for i, want := range []struct {
		name, status string // status "" = el botón Llamar
	}{
		{"Juan", ""},
		{"Pedro", i18n.T("lan.in_room")},
		{long, ""},
	} {
		row := a.lanList.Objects[i]
		labels := labelsIn(row)
		if len(labels) == 0 || labels[0].Text != want.name || labels[0].Truncation != fyne.TextTruncateEllipsis {
			t.Errorf("renglón %d: %+v", i, labels)
			continue
		}
		call := findInTree(row, buttonWithText(i18n.T("lan.call")))
		if want.status == "" {
			if call == nil || len(labels) != 1 {
				t.Errorf("renglón %d: disponible, con Llamar y sin estado", i)
			}
		} else if call != nil || len(labels) != 2 || labels[1].Text != want.status || labels[1].Importance != widget.WarningImportance {
			t.Errorf("renglón %d: en una sala, sin Llamar: %+v", i, labels)
		}
	}
	if width := a.connectionPanel.MinSize().Width; width != connectionPanelWidth {
		t.Errorf("la columna mide %v, quería %v", width, connectionPanelWidth)
	}
	if width := window.Content().MinSize().Width; width > config.DefaultWindowWidth {
		t.Errorf("un nombre largo ensancha la ventana: %v", width)
	}
	relayout(window)
	fitsDefaultWindow(t, window, "con 3")
	rowHeight := a.lanList.Objects[0].MinSize().Height
	if a.lanScroll.Size().Height < rowHeight {
		t.Errorf("con la ventana de fábrica se ve al menos un renglón: %v < %v", a.lanScroll.Size().Height, rowHeight)
	}
	saveShot(t, window, "lan_card_"+lang)

	// Con la ventana alta, los tres enteros; el registro de actividad se queda con el resto.
	window.Resize(fyne.NewSize(config.DefaultWindowWidth, 900))
	if a.lanScroll.Size().Height < a.lanList.MinSize().Height {
		t.Errorf("con la ventana alta no entran los 3: %v < %v", a.lanScroll.Size().Height, a.lanList.MinSize().Height)
	}
	saveShot(t, window, "lan_card_tall_"+lang)

	// Con más de lanRowsVisible, scroll: no le saca lugar al registro.
	var many []presence.Peer
	for i := range 6 {
		many = append(many, presence.Peer{ID: presence.ID{byte(i + 1)}, Name: "Amigo"})
	}
	a.showLANPeers(lanOn, many)
	relayout(window)
	if got, list := a.lanScroll.Size().Height, a.lanList.MinSize().Height; got >= list || got < 3*rowHeight {
		t.Errorf("con 6 se ven 3 y hace scroll: %v de %v", got, list)
	}
	window.Resize(fyne.NewSize(config.DefaultWindowWidth, config.DefaultWindowHeight))
	fitsDefaultWindow(t, window, "con 6")

	a.updateConfig(func(c *config.Config) { c.LANPresence = false })
	a.showLANPeers(lanOff, nil)
	if !a.lanHint.Visible() || a.lanHint.Text != i18n.T("lan.off") || a.lanScroll.Visible() {
		t.Errorf("apagado: %q", a.lanHint.Text)
	}
	relayout(window)
	fitsDefaultWindow(t, window, "apagado")
	a.updateConfig(func(c *config.Config) { c.LANPresence = true })
	a.showLANPeers(lanFailed, nil)
	if a.lanHint.Text != i18n.T("lan.unavailable", presence.Port) {
		t.Errorf("puerto ocupado: %q", a.lanHint.Text)
	}
	relayout(window)
	fitsDefaultWindow(t, window, "puerto ocupado")
	saveShot(t, window, "lan_card_unavailable_"+lang)

	a.hideConnectionPanels()
	if a.lanPanel.Visible() {
		t.Error("al entrar a la sala se oculta con Host y Cliente")
	}
	a.toggleConnectionPanels()
	if !a.lanPanel.Visible() {
		t.Error("☰ la vuelve a mostrar")
	}
}

// TestLANPresenceFollowsConfig: el servicio sigue el check en vivo (así "Descartar" en Ajustes lo
// deja como estaba), reintenta cada 10 s si el puerto está ocupado y no vuelve a prender al cerrar
// la app.
func TestLANPresenceFollowsConfig(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("el puerto ocupado depende de SO_EXCLUSIVEADDRUSE (Windows)")
	}
	test.NewTempApp(t)
	a := New(filepath.Join(t.TempDir(), "voxmesh.exe"))
	port := 0
	a.lan.testConfig = func(c *presence.Config) {
		c.ListenIP = netip.MustParseAddr("127.0.0.1")
		c.Port = port
		c.Targets = func() []netip.AddrPort { return nil }
	}
	defer a.stopLANPresence()
	now := time.Now()

	if !a.syncLANPresence(now) {
		t.Fatal("la app no se está cerrando")
	}
	if state, _ := a.lanSnapshot(); state != lanOn || a.lan.service == nil {
		t.Fatalf("prendido de fábrica: estado %v", state)
	}
	a.updateConfig(func(c *config.Config) { c.LANPresence = false }) // como el check de Ajustes o "Descartar"
	a.syncLANPresence(now)
	if state, _ := a.lanSnapshot(); state != lanOff || a.lan.service != nil {
		t.Fatalf("apagado: estado %v", state)
	}

	// Otro programa tiene el puerto, exclusivo (como el socket de la sala).
	key, err := transport.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	busy, err := transport.Listen("127.0.0.1:0", key)
	if err != nil {
		t.Fatal(err)
	}
	port = busy.Address().Port
	a.updateConfig(func(c *config.Config) { c.LANPresence = true })
	a.syncLANPresence(now)
	if state, _ := a.lanSnapshot(); state != lanFailed {
		t.Fatalf("con el puerto ocupado: estado %v", state)
	}
	_ = busy.Close()
	a.syncLANPresence(now.Add(5 * time.Second))
	if state, _ := a.lanSnapshot(); state != lanFailed {
		t.Errorf("reintentó antes de %v", lanRetry)
	}
	a.syncLANPresence(now.Add(lanRetry))
	if state, _ := a.lanSnapshot(); state != lanOn {
		t.Errorf("a los %v reintenta y anda: estado %v", lanRetry, state)
	}

	a.stopLANPresence()
	if a.syncLANPresence(now.Add(time.Minute)) || a.lan.service != nil {
		t.Error("cerrando la app no vuelve a prender")
	}
}

// findInTree busca, también dentro de los widgets (diálogos, popups), el primer objeto que cumple
// match.
func findInTree(root fyne.CanvasObject, match func(fyne.CanvasObject) bool) fyne.CanvasObject {
	if root == nil {
		return nil
	}
	if match(root) {
		return root
	}
	var children []fyne.CanvasObject
	switch o := root.(type) {
	case *fyne.Container:
		children = o.Objects
	case fyne.Widget:
		children = test.WidgetRenderer(o).Objects()
	}
	for _, child := range children {
		if found := findInTree(child, match); found != nil {
			return found
		}
	}
	return nil
}

// TestLANPresenceSettingsCheck: "Aparecer en mi red" está en Ajustes → Red con su ayuda (que avisa
// de Tailscale), se aplica en vivo y "Descartar" lo deja como estaba (el servicio sigue la config).
func TestLANPresenceSettingsCheck(t *testing.T) {
	test.NewTempApp(t).Settings().SetTheme(voxTheme{})
	a := New(filepath.Join(t.TempDir(), "voxmesh.exe"))
	setLanguage(t, "es")
	window := test.NewTempWindow(t, a.content())
	a.window = window
	window.Resize(fyne.NewSize(960, 700))

	tabs := openTestSettings(t, a)
	tabs.SelectIndex(2)
	param := config.ParamByKey("lan_presence_enabled")
	found := findInTree(tabs.Items[2].Content, checkWithText(paramLabel(param)))
	if found == nil {
		t.Fatal("no está el check en Red")
	}
	check := found.(*widget.Check)
	help, _ := paramHelp(param)
	if !strings.Contains(help, "Tailscale") || findInTree(tabs.Items[2].Content, labelWithText(help)) == nil {
		t.Errorf("la ayuda tiene que estar y avisar de Tailscale: %q", help)
	}
	if !check.Checked {
		t.Fatal("prendido de fábrica")
	}
	saveShot(t, window, "settings_network_es")

	test.Tap(check)
	if a.conf().LANPresence {
		t.Fatal("apagarlo se aplica en vivo")
	}
	test.Tap(footerButton(t, window, i18n.T("common.cancel")))
	discard := findInTree(window.Canvas().Overlays().Top(), buttonWithText(i18n.T("settings.discard")))
	if discard == nil {
		t.Fatal("Cancelar con cambios no preguntó")
	}
	test.Tap(discard.(*widget.Button))
	if !a.conf().LANPresence {
		t.Error("Descartar lo vuelve a prender")
	}
}
