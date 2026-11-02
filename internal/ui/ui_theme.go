//go:build cgo

package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// voxTheme es el tema oscuro de Fyne con unos pocos colores propios. Es siempre oscuro: el tema
// por defecto sigue el modo claro/oscuro de Windows, y en modo claro el texto negro quedaba
// sobre las tarjetas oscuras. Fuentes, íconos y tamaños son los de Fyne.
type voxTheme struct{}

// Colores que no son del tema de Fyne: fondo de las tarjetas (uiCard) y de los chips de la barra
// de avatares.
const (
	colorNameCard fyne.ThemeColorName = "voxCard"
	colorNameChip fyne.ThemeColorName = "voxChip"
)

var voxColors = map[fyne.ThemeColorName]color.Color{
	theme.ColorNameBackground:        color.NRGBA{R: 0x15, G: 0x17, B: 0x1b, A: 0xff},
	colorNameCard:                    color.NRGBA{R: 0x1f, G: 0x22, B: 0x28, A: 0xff},
	colorNameChip:                    color.NRGBA{R: 0x2a, G: 0x2e, B: 0x36, A: 0xff},
	theme.ColorNameButton:            color.NRGBA{R: 0x2c, G: 0x30, B: 0x38, A: 0xff},
	theme.ColorNameDisabledButton:    color.NRGBA{R: 0x25, G: 0x28, B: 0x2e, A: 0xff},
	theme.ColorNameDisabled:          color.NRGBA{R: 0x5c, G: 0x61, B: 0x6b, A: 0xff}, // el de Fyne casi no se lee
	theme.ColorNameInputBackground:   color.NRGBA{R: 0x17, G: 0x19, B: 0x1d, A: 0xff}, // más oscuro que la tarjeta
	theme.ColorNameInputBorder:       color.NRGBA{R: 0x3a, G: 0x3f, B: 0x48, A: 0xff},
	theme.ColorNameSeparator:         color.NRGBA{R: 0x33, G: 0x37, B: 0x3f, A: 0xff},
	theme.ColorNameMenuBackground:    color.NRGBA{R: 0x2a, G: 0x2e, B: 0x36, A: 0xff},
	theme.ColorNameOverlayBackground: color.NRGBA{R: 0x1f, G: 0x22, B: 0x28, A: 0xff},
	theme.ColorNameSuccess:           speakingColor, // el de Fyne es un verde chillón
}

func (voxTheme) Color(name fyne.ThemeColorName, _ fyne.ThemeVariant) color.Color {
	if c, ok := voxColors[name]; ok {
		return c
	}
	return theme.DefaultTheme().Color(name, theme.VariantDark)
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
