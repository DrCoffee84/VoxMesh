package version

import "strings"

var Value = "0.9.0-build_0"

// Commit es el commit del build (git rev-parse --short HEAD). Lo inyectan build.ps1 y release.yml
// con -X, como Value; vacío en un go build sin -X o sin git.
var Commit = ""

// IsDev dice si es un build local (build.ps1 pone "-dev.<fecha>"; un go build sin -X deja
// "-build_0"). Esos no buscan actualizaciones solos: aceptar una reemplazaba el build por el
// release y lo relanzaba.
func IsDev() bool {
	return strings.Contains(Value, "-dev") || strings.Contains(Value, "-build")
}

// CommitText es el commit para mostrar ("Acerca de"): "dev" si el build no lo trae.
func CommitText() string {
	if commit := strings.TrimSpace(Commit); commit != "" {
		return commit
	}
	return "dev"
}
