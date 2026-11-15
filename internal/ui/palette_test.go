//go:build cgo

package ui

import (
	"image/color"
	"math"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"

	"voxmesh/internal/config"
	"voxmesh/internal/i18n"
)

func nrgba(c color.Color) color.NRGBA { return color.NRGBAModel.Convert(c).(color.NRGBA) }

// Default es el look de siempre: los colores que tenía voxTheme y, lo que no definía, los del tema
// oscuro de Fyne (primario azul incluido).
func TestDefaultPaletteKeepsTheLook(t *testing.T) {
	p, _ := builtinPalette(defaultPaletteName)
	for name, want := range map[fyne.ThemeColorName]uint32{
		theme.ColorNameBackground: 0x15171b, colorNameCard: 0x1f2228, colorNameChip: 0x2a2e36,
		theme.ColorNameSuccess: 0x2ecc71, colorNameShout: 0xe63c3c, colorNameMeterFill: 0x37be7d, colorNameSharing: 0xb05414,
	} {
		if got := nrgba(p.color(name)); got != rgb(want) {
			t.Errorf("Default %s = %v, quería #%06x", name, got, want)
		}
	}
	for _, name := range []fyne.ThemeColorName{theme.ColorNamePrimary, theme.ColorNameForeground, theme.ColorNameFocus, theme.ColorNameSelection, theme.ColorNameHover} {
		if got, want := p.color(name), theme.DefaultTheme().Color(name, theme.VariantDark); got != want {
			t.Errorf("Default %s = %v, quería el de Fyne %v", name, got, want)
		}
	}
	if (voxTheme{}).Color(colorNameCard, theme.VariantLight) != p.color(colorNameCard) {
		t.Fatal("voxTheme{} tiene que ser Default (y no seguir el modo claro de Windows)")
	}
}

// Las plantillas: Naranja es Default con el resaltado naranja (y foco/selección/links que salen de
// él); Clara es clara y con el mismo naranja.
func TestPaletteTemplates(t *testing.T) {
	orange, _ := builtinPalette(orangePaletteName)
	def, _ := builtinPalette(defaultPaletteName)
	if nrgba(orange.color(theme.ColorNamePrimary)) != orangePrimary || orange.light {
		t.Fatal("Naranja: primario o base")
	}
	for _, entry := range paletteEntries {
		if entry.name != theme.ColorNamePrimary && orange.color(entry.name) != def.color(entry.name) {
			t.Errorf("Naranja cambia %s, solo tenía que cambiar el resaltado", entry.key)
		}
	}
	if focus := nrgba(orange.color(theme.ColorNameFocus)); focus.R != orangePrimary.R || focus.A != 0x7f {
		t.Fatalf("Naranja: el foco sigue azul (%v)", focus)
	}
	light, _ := builtinPalette(lightPaletteName)
	if !light.light || nrgba(light.color(theme.ColorNamePrimary)) != orangePrimary || light.variant() != theme.VariantLight {
		t.Fatal("Clara: base clara con el mismo naranja")
	}
}

// contrast es la relación de contraste WCAG entre dos colores.
func contrast(a, b color.Color) float64 {
	la, lb := luminance(nrgba(a)), luminance(nrgba(b))
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// readablePairs son los textos (y líneas) que se tienen que leer sobre su fondo.
var readablePairs = [][2]fyne.ThemeColorName{
	{theme.ColorNameForeground, theme.ColorNameBackground},
	{theme.ColorNameForeground, colorNameCard},
	{theme.ColorNameForeground, theme.ColorNameButton},
	{theme.ColorNameForeground, theme.ColorNameInputBackground},
	{theme.ColorNameForeground, colorNameChatMatch},
	{colorNameMutedText, colorNameChip},
	{colorNameAvatarText, colorNameAvatar},
	{colorNameAvatarTextSpeaking, colorNameSpeaking},
	{theme.ColorNameForegroundOnPrimary, theme.ColorNamePrimary},
	{colorNameMeterMarker, colorNameMeterBackdrop},
}

// En cada paleta de fábrica los textos se leen sobre su fondo (lo que asumía fondo oscuro, también).
func TestPalettesAreReadable(t *testing.T) {
	for _, name := range builtinPalettes {
		p, _ := builtinPalette(name)
		for _, pair := range readablePairs {
			if c := contrast(p.color(pair[0]), p.color(pair[1])); c < 2.4 {
				t.Errorf("%s: %s sobre %s tiene contraste %.1f", name, pair[0], pair[1], c)
			}
		}
	}
}

// Con el tono y la intensidad movidos (sin los colores de estado) los textos se siguen leyendo en
// cada paleta de fábrica.
func TestShiftedPalettesAreReadable(t *testing.T) {
	for _, name := range builtinPalettes {
		base, _ := builtinPalette(name)
		for _, hue := range []float64{-120, -60, 60, 120, 180} {
			for _, saturation := range []float64{0, 1, 2} {
				draft := newPaletteDraft(base)
				draft.hue, draft.saturation = hue, saturation
				p := draft.palette(name)
				for _, pair := range readablePairs {
					if c := contrast(p.color(pair[0]), p.color(pair[1])); c < 2.4 {
						t.Errorf("%s con %+.0f° y %.0f %%: %s sobre %s tiene contraste %.1f", name, hue, saturation*100, pair[0], pair[1], c)
					}
				}
			}
		}
	}
}

// shiftHue gira el tono sin tocar la luz ni el alfa; un gris no cambia.
func TestShiftHue(t *testing.T) {
	colors := []color.NRGBA{orangePrimary, rgb(0x15171b), rgb(0x2ecc71), rgb(0x3c4655), {R: 0x11, G: 0x22, B: 0x33, A: 0x44}}
	near := func(a, b color.NRGBA) bool {
		d := func(x, y uint8) bool { return x-y <= 1 || y-x <= 1 }
		return d(a.R, b.R) && d(a.G, b.G) && d(a.B, b.B) && a.A == b.A
	}
	for _, c := range colors {
		if got := shiftHue(c, 0, 1); got != c {
			t.Errorf("%s con 0°: %s", formatHex(c), formatHex(got))
		}
		if got := shiftHue(c, 360, 1); !near(got, c) {
			t.Errorf("%s con 360°: %s", formatHex(c), formatHex(got))
		}
		if a, b := shiftHue(c, -90, 1), shiftHue(c, 270, 1); !near(a, b) {
			t.Errorf("%s: −90° %s y 270° %s", formatHex(c), formatHex(a), formatHex(b))
		}
		_, _, l := rgbToHSL(c)
		for _, degrees := range []float64{45, 137, -100} {
			got := shiftHue(c, degrees, 1)
			if _, _, gl := rgbToHSL(got); math.Abs(gl-l) > 1.0/255 || got.A != c.A {
				t.Errorf("%s con %+.0f°: %s cambió la luz o el alfa", formatHex(c), degrees, formatHex(got))
			}
		}
		if gray := shiftHue(c, 0, 0); gray.R != gray.G || gray.G != gray.B || gray.A != c.A {
			t.Errorf("%s con intensidad 0: %s no es gris", formatHex(c), formatHex(gray))
		}
	}
	for _, gray := range []color.NRGBA{rgb(0x000000), rgb(0x808080), rgb(0xf3f3f3), rgb(0xffffff)} {
		if got := shiftHue(gray, 137, 2); got != gray {
			t.Errorf("gris %s: %s", formatHex(gray), formatHex(got))
		}
	}
	if blue := shiftHue(orangePrimary, 180, 1); !(blue.B > blue.G && blue.G > blue.R) {
		t.Errorf("#fb8c00 con 180°: %s, quería un azul", formatHex(blue))
	}
	_, s, _ := rgbToHSL(rgb(0x806060))
	if _, s2, _ := rgbToHSL(shiftHue(rgb(0x806060), 0, 2)); math.Abs(s2-2*s) > 0.01 {
		t.Errorf("intensidad 200 %%: saturación %.2f, quería %.2f", s2, 2*s)
	}
}

// El borrador: ir y volver deja los colores exactos, un color elegido de a uno gira con los demás
// desde donde se eligió, y los de estado solo se mueven con la casilla.
func TestPaletteDraftHue(t *testing.T) {
	orange, _ := builtinPalette(orangePaletteName)
	d := newPaletteDraft(orange)
	base := map[fyne.ThemeColorName]color.NRGBA{}
	for _, entry := range paletteEntries {
		base[entry.name] = d.shown(entry.name)
	}
	d.hue, d.saturation = 120, 1.5
	if d.shown(theme.ColorNamePrimary) == orangePrimary || d.shown(colorNameCard) == base[colorNameCard] {
		t.Fatal("el tono no movió el resaltado o las tarjetas")
	}
	for name := range paletteStatusColors {
		if d.shown(name) != base[name] {
			t.Errorf("%s se movió sin la casilla", name)
		}
	}
	d.moveStatus = true
	if d.shown(theme.ColorNameError) == base[theme.ColorNameError] {
		t.Fatal("con la casilla, el error no se movió")
	}
	d.hue, d.saturation, d.moveStatus = 0, 1, false
	for _, entry := range paletteEntries {
		if d.shown(entry.name) != base[entry.name] {
			t.Errorf("%s no volvió exacto: %s, era %s", entry.key, formatHex(d.shown(entry.name)), formatHex(base[entry.name]))
		}
	}

	// Elegido con el tono en +60: se ve tal cual y gira con los demás.
	picked := rgb(0x123456)
	d.hue = 60
	d.pick(colorNameCard, picked)
	if d.shown(colorNameCard) != picked {
		t.Fatalf("elegido: %s", formatHex(d.shown(colorNameCard)))
	}
	d.hue = -30
	if got, want := d.shown(colorNameCard), shiftHue(picked, -90, 1); got != want {
		t.Fatalf("con −30°: %s, quería %s", formatHex(got), formatHex(want))
	}
	d.hue = 60
	if d.shown(colorNameCard) != picked {
		t.Fatal("al volver a +60° no es el elegido")
	}
	// Con la intensidad en 50 %: al 100 % tiene el doble de saturación.
	d.hue, d.saturation = 0, 0.5
	d.pick(colorNameChip, rgb(0xa04040))
	d.saturation = 1
	if got, want := d.shown(colorNameChip), shiftHue(rgb(0xa04040), 0, 2); got != want {
		t.Fatalf("intensidad: %s, quería %s", formatHex(got), formatHex(want))
	}
	// Uno de estado elegido sin la casilla queda como se eligió; con la casilla gira desde 0°.
	d.hue = 60
	d.pick(theme.ColorNameError, rgb(0xff0000))
	d.hue = -30
	if d.shown(theme.ColorNameError) != rgb(0xff0000) {
		t.Fatal("el error elegido se movió sin la casilla")
	}
	d.moveStatus = true
	if got, want := d.shown(theme.ColorNameError), shiftHue(rgb(0xff0000), -30, 1); got != want {
		t.Fatalf("error con la casilla: %s, quería %s", formatHex(got), formatHex(want))
	}
	// Se guarda lo que se ve.
	if file := d.palette("Mía").file("Mía"); file.Colors["error"] != formatHex(d.shown(theme.ColorNameError)) {
		t.Fatalf("archivo: %s", file.Colors["error"])
	}
}

// El editor: los sliders mueven las muestras, no la app hasta "Aplicar"; "Cerrar" la deja como
// estaba.
func TestPaletteEditorHue(t *testing.T) {
	test.NewTempApp(t).Settings().SetTheme(voxTheme{})
	a := newAppWithOwnConfig(t)
	a.palettes.Dir = t.TempDir()
	a.window = test.NewTempWindow(t, a.content())
	a.applyPalette(a.readPalette(orangePaletteName))
	bar, _ := a.newPaletteBar(nil)
	e := a.newPaletteEditor(bar)
	e.dialog.Show()
	swatch := func(name fyne.ThemeColorName) color.Color {
		for i, entry := range paletteEntries {
			if entry.name == name {
				return e.swatches[i].FillColor
			}
		}
		t.Fatalf("sin muestra para %s", name)
		return nil
	}
	errorColor := swatch(theme.ColorNameError)

	e.hue.SetValue(120)
	e.saturation.SetValue(150)
	want := shiftHue(orangePrimary, 120, 1.5)
	if swatch(theme.ColorNamePrimary) != color.Color(want) {
		t.Fatalf("muestra del resaltado: %v, quería %s", swatch(theme.ColorNamePrimary), formatHex(want))
	}
	if e.hueLabel.Text != i18n.T("palette.hue", "+120") || e.saturationLabel.Text != i18n.T("palette.saturation", 150) {
		t.Fatalf("textos: %q, %q", e.hueLabel.Text, e.saturationLabel.Text)
	}
	if nrgba(theme.Color(theme.ColorNamePrimary)) != orangePrimary {
		t.Fatal("la app cambió antes de Aplicar")
	}
	if swatch(theme.ColorNameError) != errorColor {
		t.Fatal("el error se movió sin la casilla")
	}
	test.Tap(e.moveStatus)
	if swatch(theme.ColorNameError) == errorColor {
		t.Fatal("con la casilla, la muestra del error no se movió")
	}

	test.Tap(e.applyButton)
	if nrgba(theme.Color(theme.ColorNamePrimary)) != want {
		t.Fatalf("Aplicar: %v", theme.Color(theme.ColorNamePrimary))
	}
	test.Tap(e.closeButton)
	if nrgba(theme.Color(theme.ColorNamePrimary)) != orangePrimary || a.palette.name != orangePaletteName {
		t.Fatal("Cerrar sin guardar no volvió a Naranja")
	}
	// Al reabrir, los sliders arrancan en 0° y 100 %.
	if e := a.newPaletteEditor(bar); e.hue.Value != 0 || e.saturation.Value != 100 || e.draft.shown(theme.ColorNamePrimary) != orangePrimary {
		t.Fatal("el editor no arrancó de cero")
	}
}

// Una paleta guardada vuelve igual; un color que falta o no se entiende toma el de la base.
func TestPaletteFileRoundTrip(t *testing.T) {
	light, _ := builtinPalette(lightPaletteName)
	file := light.file("Mía")
	back := paletteFromFile(file)
	if !back.light || back.name != "Mía" {
		t.Fatalf("base %v, nombre %q", back.light, back.name)
	}
	for _, entry := range paletteEntries {
		if nrgba(back.color(entry.name)) != nrgba(light.color(entry.name)) {
			t.Errorf("%s: %v, quería %v", entry.key, back.color(entry.name), light.color(entry.name))
		}
	}
	file.Colors["card"] = "no es un color"
	delete(file.Colors, "chip")
	back = paletteFromFile(file)
	if back.color(colorNameCard) != light.color(colorNameCard) || back.color(colorNameChip) != light.color(colorNameChip) {
		t.Fatal("un color roto o que falta tiene que salir de la base")
	}
	for text, ok := range map[string]bool{"#fb8c00": true, "FB8C00": true, "#11223344": true, "#123": false, "#gggggg": false, "": false} {
		if c, got := parseHex(text); got != ok || (ok && formatHex(c) != map[bool]string{true: "#11223344", false: "#fb8c00"}[len(text) == 9]) {
			t.Errorf("parseHex(%q) = %v, %v", text, c, got)
		}
	}
}

func TestPaletteNames(t *testing.T) {
	for name, want := range map[string]string{"": defaultPaletteName, "default": defaultPaletteName, "orange": orangePaletteName, "NARANJA": orangePaletteName, "Light": lightPaletteName, "clara": lightPaletteName, "Mía": "Mía"} {
		if got := canonicalPaletteName(name); got != want {
			t.Errorf("canonicalPaletteName(%q) = %q, quería %q", name, got, want)
		}
	}
	if _, err := validatePaletteName("Orange"); err != config.ErrPaletteNameReserved {
		t.Fatalf("Orange: %v", err)
	}
}

// Aplicar una paleta cambia los colores en vivo: los widgets, las tarjetas (themedRect) y los chips.
func TestApplyPaletteLive(t *testing.T) {
	test.NewTempApp(t).Settings().SetTheme(voxTheme{})
	a := newAppWithOwnConfig(t)
	a.palettes.Dir = t.TempDir()
	window := test.NewTempWindow(t, a.content())
	window.Resize(fyne.NewSize(960, 600))
	a.peerNames = []peerView{{id: "aaaaaaaaaaaaaaaa", name: "Yo", isSelf: true}}
	a.refreshPeerAvatars()
	chip := a.peerChips[0]

	light := a.readPalette("Light")
	a.applyPalette(light)
	if theme.Color(theme.ColorNameBackground) != light.color(theme.ColorNameBackground) || a.palette.name != lightPaletteName {
		t.Fatal("el tema no cambió")
	}
	if len(a.peerChips) != 1 || a.peerChips[0] == chip {
		t.Fatal("los chips no se volvieron a armar")
	}
	if got := a.voiceIndicator.fill; got != colorNameIdle {
		t.Fatalf("indicador de voz con %q", got)
	}

	// Una propia: se guarda, se lee y se aplica; si falta el archivo, Default.
	mine := light.file("Mía")
	mine.Colors["primary"] = "#00ff00"
	if err := a.palettes.Save(mine, paletteReservedNames()); err != nil {
		t.Fatal(err)
	}
	if p := a.readPalette("Mía"); nrgba(p.color(theme.ColorNamePrimary)) != rgb(0x00ff00) || !p.light {
		t.Fatalf("Mía: %v", p.color(theme.ColorNamePrimary))
	}
	if p := a.readPalette("No existe"); p.name != defaultPaletteName {
		t.Fatalf("sin archivo: %q", p.name)
	}
	a.applyPalette(a.readPalette(""))
	if theme.Color(colorNameCard) != rgb(0x1f2228) {
		t.Fatal("no volvió a Default")
	}
}
