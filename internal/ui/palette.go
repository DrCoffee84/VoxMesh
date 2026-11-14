//go:build cgo

package ui

import (
	"fmt"
	"image/color"
	"math"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"voxmesh/internal/config"
	"voxmesh/internal/i18n"
)

// Paletas de colores (AGENDA C15). Todo color de la app sale del tema por nombre (los de Fyne y
// los colorName… de abajo), así elegir una paleta los cambia todos sin reiniciar: Fyne refresca
// cada widget al cambiar el tema (SetTheme), y lo que se pinta con un color guardado (canvas) es
// un themedRect o se vuelve a armar en applyPalette.
//
// De fábrica: Default (los colores de siempre), Naranja (Default con el resaltado naranja) y Clara
// (fondos claros, texto oscuro, el mismo naranja). Propias: <AppDir>/paletas (config.Palettes).

// Colores propios (además de los de Fyne).
const (
	colorNameCard               fyne.ThemeColorName = "voxCard"               // fondo de las tarjetas (uiCard)
	colorNameChip               fyne.ThemeColorName = "voxChip"               // chips de la barra de participantes
	colorNameMutedText          fyne.ThemeColorName = "voxMutedText"          // texto secundario de los chips (lag, volumen)
	colorNameSpeaking           fyne.ThemeColorName = "voxSpeaking"           // "hablando": avatar, voz propia, ping bueno
	colorNameShout              fyne.ThemeColorName = "voxShout"              // modo grito: aro del avatar, medidor
	colorNameIdle               fyne.ThemeColorName = "voxIdle"               // indicador de voz propia apagado
	colorNameAvatar             fyne.ThemeColorName = "voxAvatar"             // círculo del avatar de quien no habla
	colorNameAvatarText         fyne.ThemeColorName = "voxAvatarText"         // inicial del avatar
	colorNameAvatarTextSpeaking fyne.ThemeColorName = "voxAvatarTextSpeaking" // inicial mientras habla
	colorNameSharing            fyne.ThemeColorName = "voxSharing"            // banner e insignia de pantalla compartida
	colorNameMeterFill          fyne.ThemeColorName = "voxMeterFill"          // medidor: se manda (verde)
	colorNameMeterHeld          fyne.ThemeColorName = "voxMeterHeld"          // medidor: hay sonido y no se manda (amarillo)
	colorNameMeterQuiet         fyne.ThemeColorName = "voxMeterQuiet"         // medidor: silencio (gris)
	colorNameMeterBackdrop      fyne.ThemeColorName = "voxMeterBackdrop"      // fondo de las barras de nivel
	colorNameMeterMarker        fyne.ThemeColorName = "voxMeterMarker"        // línea de umbral de las barras
	colorNameChatMatch          fyne.ThemeColorName = "voxChatMatch"          // mensaje que coincide con la búsqueda
)

// paletteEntry es un color de la paleta: su clave en el archivo, su nombre en el tema y su texto
// (clave de i18n) en el editor, en ese orden.
type paletteEntry struct {
	key   string
	name  fyne.ThemeColorName
	label string
}

var paletteEntries = []paletteEntry{
	{"primary", theme.ColorNamePrimary, "palette.color.primary"},
	{"background", theme.ColorNameBackground, "palette.color.background"},
	{"card", colorNameCard, "palette.color.card"},
	{"chip", colorNameChip, "palette.color.chip"},
	{"button", theme.ColorNameButton, "palette.color.button"},
	{"disabled_button", theme.ColorNameDisabledButton, "palette.color.disabled_button"},
	{"input", theme.ColorNameInputBackground, "palette.color.input"},
	{"input_border", theme.ColorNameInputBorder, "palette.color.input_border"},
	{"separator", theme.ColorNameSeparator, "palette.color.separator"},
	{"menu", theme.ColorNameMenuBackground, "palette.color.menu"},
	{"overlay", theme.ColorNameOverlayBackground, "palette.color.overlay"},
	{"foreground", theme.ColorNameForeground, "palette.color.foreground"},
	{"muted_text", colorNameMutedText, "palette.color.muted_text"},
	{"disabled", theme.ColorNameDisabled, "palette.color.disabled"},
	{"success", theme.ColorNameSuccess, "palette.color.success"},
	{"warning", theme.ColorNameWarning, "palette.color.warning"},
	{"error", theme.ColorNameError, "palette.color.error"},
	{"speaking", colorNameSpeaking, "palette.color.speaking"},
	{"shout", colorNameShout, "palette.color.shout"},
	{"idle", colorNameIdle, "palette.color.idle"},
	{"avatar", colorNameAvatar, "palette.color.avatar"},
	{"avatar_text", colorNameAvatarText, "palette.color.avatar_text"},
	{"avatar_text_speaking", colorNameAvatarTextSpeaking, "palette.color.avatar_text_speaking"},
	{"sharing", colorNameSharing, "palette.color.sharing"},
	{"meter", colorNameMeterFill, "palette.color.meter"},
	{"meter_held", colorNameMeterHeld, "palette.color.meter_held"},
	{"meter_quiet", colorNameMeterQuiet, "palette.color.meter_quiet"},
	{"meter_backdrop", colorNameMeterBackdrop, "palette.color.meter_backdrop"},
	{"meter_marker", colorNameMeterMarker, "palette.color.meter_marker"},
	{"chat_match", colorNameChatMatch, "palette.color.chat_match"},
}

// Paletas de fábrica: claves fijas (se guardan así en voxmesh.json; en pantalla, traducidas).
const (
	defaultPaletteName = "Default"
	orangePaletteName  = "Naranja"
	lightPaletteName   = "Clara"
)

var builtinPalettes = []string{defaultPaletteName, orangePaletteName, lightPaletteName}

// orangePrimary es el naranja del resaltado de las plantillas.
var orangePrimary = rgb(0xfb8c00)

// palette es una paleta: base clara u oscura y los colores que define; lo que no define sale del
// tema de Fyne de esa base.
type palette struct {
	name   string // clave de una de fábrica o nombre de una propia
	light  bool
	colors map[fyne.ThemeColorName]color.Color
}

func rgb(hex uint32) color.NRGBA {
	return color.NRGBA{R: uint8(hex >> 16), G: uint8(hex >> 8), B: uint8(hex), A: 0xff}
}

// defaultColors son los colores de siempre (los de antes de las paletas). Primario, texto,
// advertencia y error no están: son los del tema oscuro de Fyne.
var defaultColors = map[fyne.ThemeColorName]color.Color{
	theme.ColorNameBackground:        rgb(0x15171b),
	colorNameCard:                    rgb(0x1f2228),
	colorNameChip:                    rgb(0x2a2e36),
	theme.ColorNameButton:            rgb(0x2c3038),
	theme.ColorNameDisabledButton:    rgb(0x25282e),
	theme.ColorNameDisabled:          rgb(0x5c616b), // el de Fyne casi no se lee
	theme.ColorNameInputBackground:   rgb(0x17191d), // más oscuro que la tarjeta
	theme.ColorNameInputBorder:       rgb(0x3a3f48),
	theme.ColorNameSeparator:         rgb(0x33373f),
	theme.ColorNameMenuBackground:    rgb(0x2a2e36),
	theme.ColorNameOverlayBackground: rgb(0x1f2228),
	theme.ColorNameSuccess:           rgb(0x2ecc71), // el de Fyne es un verde chillón
	colorNameMutedText:               rgb(0xa0aab9),
	colorNameSpeaking:                rgb(0x2ecc71),
	colorNameShout:                   rgb(0xe63c3c),
	colorNameIdle:                    rgb(0x646e78),
	colorNameAvatar:                  rgb(0x3c4655),
	colorNameAvatarText:              rgb(0xf0f5ff),
	colorNameAvatarTextSpeaking:      rgb(0x0f230f),
	colorNameSharing:                 rgb(0xb05414), // naranja oscuro que se ve sin parecer un error
	colorNameMeterFill:               rgb(0x37be7d),
	colorNameMeterHeld:               rgb(0xebbe3c),
	colorNameMeterQuiet:              rgb(0x69707d),
	colorNameMeterBackdrop:           rgb(0x323741),
	colorNameMeterMarker:             rgb(0xebebf0),
	colorNameChatMatch:               rgb(0x46411e),
}

// lightColors es la plantilla Clara: la de siempre "invertida" (fondos claros, texto oscuro), con
// el mismo naranja; los colores que asumían fondo oscuro (medidores, avatar, insignias) tienen su
// versión para fondo claro.
var lightColors = map[fyne.ThemeColorName]color.Color{
	theme.ColorNamePrimary:           orangePrimary,
	theme.ColorNameBackground:        rgb(0xf2f3f5),
	colorNameCard:                    rgb(0xffffff),
	colorNameChip:                    rgb(0xe9ecf0),
	theme.ColorNameButton:            rgb(0xe3e6ea),
	theme.ColorNameDisabledButton:    rgb(0xeceef1),
	theme.ColorNameDisabled:          rgb(0x9aa1ab),
	theme.ColorNameInputBackground:   rgb(0xf7f8fa),
	theme.ColorNameInputBorder:       rgb(0xc5cbd3),
	theme.ColorNameSeparator:         rgb(0xd8dce2),
	theme.ColorNameMenuBackground:    rgb(0xffffff),
	theme.ColorNameOverlayBackground: rgb(0xffffff),
	theme.ColorNameForeground:        rgb(0x1f2328),
	theme.ColorNameSuccess:           rgb(0x1f9d55),
	theme.ColorNameWarning:           rgb(0xc27c0e),
	theme.ColorNameError:             rgb(0xd93025),
	colorNameMutedText:               rgb(0x5b6573),
	colorNameSpeaking:                rgb(0x2ecc71),
	colorNameShout:                   rgb(0xe03131),
	colorNameIdle:                    rgb(0x9aa3ad),
	colorNameAvatar:                  rgb(0x6b7a90),
	colorNameAvatarText:              rgb(0xffffff),
	colorNameAvatarTextSpeaking:      rgb(0x0f230f),
	colorNameSharing:                 rgb(0xc2560f),
	colorNameMeterFill:               rgb(0x2fa36b),
	colorNameMeterHeld:               rgb(0xe0a800),
	colorNameMeterQuiet:              rgb(0xa3aab5),
	colorNameMeterBackdrop:           rgb(0xdde1e6),
	colorNameMeterMarker:             rgb(0x1f2328),
	colorNameChatMatch:               rgb(0xffefc2),
}

// builtinPalette devuelve la paleta de fábrica name (por su clave).
func builtinPalette(name string) (palette, bool) {
	switch name {
	case defaultPaletteName:
		return palette{name: name, colors: defaultColors}, true
	case orangePaletteName:
		colors := make(map[fyne.ThemeColorName]color.Color, len(defaultColors)+1)
		for k, v := range defaultColors {
			colors[k] = v
		}
		colors[theme.ColorNamePrimary] = orangePrimary
		return palette{name: name, colors: colors}, true
	case lightPaletteName:
		return palette{name: name, light: true, colors: lightColors}, true
	}
	return palette{}, false
}

// paletteLabelKey es la clave de i18n del nombre de una paleta de fábrica ("" para Default, que no
// se traduce).
func paletteLabelKey(name string) string {
	switch name {
	case orangePaletteName:
		return "palette.name.orange"
	case lightPaletteName:
		return "palette.name.light"
	}
	return ""
}

// paletteLabel es cómo se ve una paleta en el selector (las de fábrica, traducidas).
func paletteLabel(name string) string {
	if key := paletteLabelKey(name); key != "" {
		return i18n.T(key)
	}
	return name
}

// canonicalPaletteName lleva "" y cualquier nombre de una de fábrica (en cualquier idioma, sin
// mayúsculas) a su clave; el resto queda igual.
func canonicalPaletteName(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return defaultPaletteName
	}
	for _, builtin := range builtinPalettes {
		if strings.EqualFold(trimmed, builtin) {
			return builtin
		}
		if key := paletteLabelKey(builtin); key != "" {
			for _, language := range i18n.Languages {
				if strings.EqualFold(trimmed, i18n.In(language.Code, key)) {
					return builtin
				}
			}
		}
	}
	return name
}

func isBuiltinPalette(name string) bool {
	_, ok := builtinPalette(canonicalPaletteName(name))
	return ok
}

// paletteReservedNames son los nombres que no puede tener una paleta propia.
func paletteReservedNames() []string {
	names := append([]string(nil), builtinPalettes...)
	for _, builtin := range builtinPalettes {
		if key := paletteLabelKey(builtin); key != "" {
			for _, language := range i18n.Languages {
				names = append(names, i18n.In(language.Code, key))
			}
		}
	}
	return names
}

// variant es la base de Fyne de la paleta.
func (p palette) variant() fyne.ThemeVariant {
	if p.light {
		return theme.VariantLight
	}
	return theme.VariantDark
}

// color es el color que usa la app para name con esta paleta. Si la paleta define el primario,
// el foco, la selección, los links y el texto sobre el primario salen de él (Fyne los deriva de
// su primario con nombre, que es otro).
func (p palette) color(name fyne.ThemeColorName) color.Color {
	if c, ok := p.colors[name]; ok {
		return c
	}
	if primary, ok := p.colors[theme.ColorNamePrimary]; ok {
		base := color.NRGBAModel.Convert(primary).(color.NRGBA)
		switch name {
		case theme.ColorNameFocus:
			base.A = 0x7f
			return base
		case theme.ColorNameSelection:
			base.A = 0x3f
			return base
		case theme.ColorNameHyperlink:
			return base
		case theme.ColorNameForegroundOnPrimary:
			// El que más contraste tenga (blanco sobre el naranja casi no se lee: 2.4 contra 6.7).
			dark, light := rgb(0x1f2328), rgb(0xffffff)
			l := luminance(base)
			if (l+0.05)/(luminance(dark)+0.05) > 1.05/(l+0.05) {
				return dark
			}
			return light
		}
	}
	return theme.DefaultTheme().Color(name, p.variant())
}

// luminance es la luminancia relativa (0–1, WCAG) de c.
func luminance(c color.NRGBA) float64 {
	channel := func(v uint8) float64 {
		x := float64(v) / 255
		if x <= 0.03928 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(c.R) + 0.7152*channel(c.G) + 0.0722*channel(c.B)
}

// file pasa la paleta a su archivo con todos los colores de paletteEntries (los efectivos).
func (p palette) file(name string) config.PaletteFile {
	file := config.PaletteFile{Name: name, Light: p.light, Colors: make(map[string]string, len(paletteEntries))}
	for _, entry := range paletteEntries {
		file.Colors[entry.key] = formatHex(p.color(entry.name))
	}
	return file
}

// paletteFromFile arma una paleta propia: parte de Default (o de Clara si es clara) y le pone los
// colores del archivo que se entienden.
func paletteFromFile(file config.PaletteFile) palette {
	base, _ := builtinPalette(defaultPaletteName)
	if file.Light {
		base, _ = builtinPalette(lightPaletteName)
	}
	colors := make(map[fyne.ThemeColorName]color.Color, len(paletteEntries))
	for _, entry := range paletteEntries {
		if c, ok := parseHex(file.Colors[entry.key]); ok {
			colors[entry.name] = c
		} else if c, ok := base.colors[entry.name]; ok {
			colors[entry.name] = c
		}
	}
	return palette{name: file.Name, light: file.Light, colors: colors}
}

// formatHex escribe "#rrggbb" ("#rrggbbaa" si no es opaco).
func formatHex(c color.Color) string {
	n := color.NRGBAModel.Convert(c).(color.NRGBA)
	if n.A == 0xff {
		return fmt.Sprintf("#%02x%02x%02x", n.R, n.G, n.B)
	}
	return fmt.Sprintf("#%02x%02x%02x%02x", n.R, n.G, n.B, n.A)
}

// parseHex lee "#rrggbb" o "#rrggbbaa".
func parseHex(text string) (color.NRGBA, bool) {
	text = strings.TrimPrefix(strings.TrimSpace(text), "#")
	if len(text) != 6 && len(text) != 8 {
		return color.NRGBA{}, false
	}
	var value uint64
	for _, r := range text {
		var digit uint64
		switch {
		case r >= '0' && r <= '9':
			digit = uint64(r - '0')
		case r >= 'a' && r <= 'f':
			digit = uint64(r-'a') + 10
		case r >= 'A' && r <= 'F':
			digit = uint64(r-'A') + 10
		default:
			return color.NRGBA{}, false
		}
		value = value<<4 | digit
	}
	if len(text) == 6 {
		value = value<<8 | 0xff
	}
	return color.NRGBA{R: uint8(value >> 24), G: uint8(value >> 16), B: uint8(value >> 8), A: uint8(value)}, true
}

// voxTheme es el tema de la app: la paleta activa sobre el de Fyne (fuentes, íconos y tamaños,
// los de Fyne). El valor cero es Default. No sigue el modo claro/oscuro de Windows: lo decide la
// paleta (en modo claro el texto negro quedaba sobre las tarjetas oscuras).
type voxTheme struct {
	p *palette
}

func (t voxTheme) palette() palette {
	if t.p != nil {
		return *t.p
	}
	p, _ := builtinPalette(defaultPaletteName)
	return p
}

func (t voxTheme) Color(name fyne.ThemeColorName, _ fyne.ThemeVariant) color.Color {
	return t.palette().color(name)
}

func (voxTheme) Font(style fyne.TextStyle) fyne.Resource {
	return theme.DefaultTheme().Font(style)
}

func (voxTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(name)
}

func (voxTheme) Size(name fyne.ThemeSizeName) float32 {
	return theme.DefaultTheme().Size(name)
}

// Colores de la app con nombre (del tema vigente): leerlos cuando se pinta, no guardarlos.
func speakingColor() color.Color  { return theme.Color(colorNameSpeaking) }
func shoutColor() color.Color     { return theme.Color(colorNameShout) }
func idleColor() color.Color      { return theme.Color(colorNameIdle) }
func avatarColor() color.Color    { return theme.Color(colorNameAvatar) }
func mutedTextColor() color.Color { return theme.Color(colorNameMutedText) }
func levelFillColor() color.Color { return theme.Color(colorNameMeterFill) }
func meterQuietColor() color.Color {
	return theme.Color(colorNameMeterQuiet)
}
func meterHeldColor() color.Color   { return theme.Color(colorNameMeterHeld) }
func meterMarkerColor() color.Color { return theme.Color(colorNameMeterMarker) }

// meterMarkerOffColor es la línea de umbral tenue (puerta o VAD apagados).
func meterMarkerOffColor() color.Color {
	c := color.NRGBAModel.Convert(meterMarkerColor()).(color.NRGBA)
	c.A = 70
	return c
}

// themedRect es un rectángulo (o círculo) que toma sus colores del tema por nombre en cada
// Refresh: sigue un cambio de paleta solo (Fyne refresca todos los widgets al cambiar el tema).
// Los objetos de canvas comunes guardan el color con el que se crearon.
type themedRect struct {
	widget.BaseWidget
	fill, stroke fyne.ThemeColorName // "" = transparente / sin borde
	strokeWidth  float32
	radius       float32
	circle       bool
}

func newThemedRect(fill fyne.ThemeColorName, radius float32) *themedRect {
	r := &themedRect{fill: fill, radius: radius}
	r.ExtendBaseWidget(r)
	return r
}

func newThemedCircle(fill fyne.ThemeColorName) *themedRect {
	r := &themedRect{fill: fill, circle: true}
	r.ExtendBaseWidget(r)
	return r
}

// setFill cambia el color de relleno (hilo de UI).
func (r *themedRect) setFill(name fyne.ThemeColorName) {
	if r.fill != name {
		r.fill = name
		r.Refresh()
	}
}

func (r *themedRect) CreateRenderer() fyne.WidgetRenderer {
	renderer := &themedRectRenderer{owner: r}
	if r.circle {
		renderer.circle = canvas.NewCircle(color.Transparent)
		renderer.object = renderer.circle
	} else {
		renderer.rect = canvas.NewRectangle(color.Transparent)
		renderer.object = renderer.rect
	}
	renderer.Refresh()
	return renderer
}

type themedRectRenderer struct {
	owner  *themedRect
	rect   *canvas.Rectangle
	circle *canvas.Circle
	object fyne.CanvasObject
}

func themeColorOrClear(name fyne.ThemeColorName) color.Color {
	if name == "" {
		return color.Transparent
	}
	return theme.Color(name)
}

func (r *themedRectRenderer) Layout(size fyne.Size)        { r.object.Resize(size) }
func (r *themedRectRenderer) MinSize() fyne.Size           { return fyne.NewSize(0, 0) }
func (r *themedRectRenderer) Objects() []fyne.CanvasObject { return []fyne.CanvasObject{r.object} }
func (r *themedRectRenderer) Destroy()                     {}

func (r *themedRectRenderer) Refresh() {
	fill, stroke := themeColorOrClear(r.owner.fill), themeColorOrClear(r.owner.stroke)
	if r.circle != nil {
		r.circle.FillColor, r.circle.StrokeColor, r.circle.StrokeWidth = fill, stroke, r.owner.strokeWidth
	} else {
		r.rect.FillColor, r.rect.StrokeColor, r.rect.StrokeWidth = fill, stroke, r.owner.strokeWidth
		r.rect.CornerRadius = r.owner.radius
	}
	r.object.Refresh()
}
