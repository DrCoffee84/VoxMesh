package version

import "strings"

var Value = "0.9.0-build_0"

// IsDev dice si es un build local (build.ps1 pone "-dev.<fecha>"; un go build sin -X deja
// "-build_0"). Esos no buscan actualizaciones solos: aceptar una reemplazaba el build por el
// release y lo relanzaba.
func IsDev() bool {
	return strings.Contains(Value, "-dev") || strings.Contains(Value, "-build")
}
