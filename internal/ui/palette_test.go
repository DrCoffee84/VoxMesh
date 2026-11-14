//go:build cgo

package ui

import (
	"image/color"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"

	"voxmesh/internal/config"
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

// En cada paleta de fábrica los textos se leen sobre su fondo (lo que asumía fondo oscuro, también).
func TestPalettesAreReadable(t *testing.T) {
	pairs := [][2]fyne.ThemeColorName{
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
	for _, name := range builtinPalettes {
		p, _ := builtinPalette(name)
		for _, pair := range pairs {
			if c := contrast(p.color(pair[0]), p.color(pair[1])); c < 2.4 {
				t.Errorf("%s: %s sobre %s tiene contraste %.1f", name, pair[0], pair[1], c)
			}
		}
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
