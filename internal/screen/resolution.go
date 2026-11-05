package screen

import "slices"

// Resolution es el tamaño con el que sale la pantalla compartida: se elige en el diálogo de
// "Compartir" y se guarda en la config como texto (screen_resolution). Es un tope de ancho: un
// monitor más angosto que la opción sale a su tamaño (nunca se agranda).
type Resolution string

const (
	ResolutionFull Resolution = "full"  // sin tope; solo se ofrece si el monitor pasa de DefaultMaxWidth
	Resolution1080 Resolution = "1080p" // 1920 de ancho
	Resolution900  Resolution = "900p"  // 1600
	Resolution720  Resolution = "720p"  // 1280

	// DefaultResolution es la de siempre (DefaultMaxWidth): si no se toca nada, nada cambia.
	DefaultResolution = Resolution1080
)

// ParseResolution lee la opción guardada: vacía (config de antes) o desconocida → DefaultResolution.
func ParseResolution(s string) Resolution {
	switch r := Resolution(s); r {
	case ResolutionFull, Resolution1080, Resolution900, Resolution720:
		return r
	}
	return DefaultResolution
}

// MaxWidth es el tope para Sender.MaxWidth (0 = resolución completa).
func (r Resolution) MaxWidth() int {
	switch r {
	case ResolutionFull:
		return 0
	case Resolution900:
		return 1600
	case Resolution720:
		return 1280
	}
	return DefaultMaxWidth
}

// Resolutions son las opciones para un monitor de ese ancho, de la más grande a la más chica.
// "Completa" solo si pasa de DefaultMaxWidth: si no, sale igual que 1080p.
func Resolutions(monitorWidth int) []Resolution {
	if monitorWidth > DefaultMaxWidth {
		return []Resolution{ResolutionFull, Resolution1080, Resolution900, Resolution720}
	}
	return []Resolution{Resolution1080, Resolution900, Resolution720}
}

// OfferedResolution es la que se muestra elegida para un monitor: la preferida si ese monitor la
// ofrece, si no DefaultResolution (Completa en un monitor de hasta 1920 sale igual que 1080p).
func OfferedResolution(preferred Resolution, monitorWidth int) Resolution {
	if slices.Contains(Resolutions(monitorWidth), preferred) {
		return preferred
	}
	return DefaultResolution
}
