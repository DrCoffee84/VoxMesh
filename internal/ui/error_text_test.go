//go:build cgo

package ui

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"voxmesh/internal/i18n"
	"voxmesh/internal/invite"
)

// En español el mensaje es exactamente el texto del error (lo que se veía y se loguea no cambia);
// en inglés sale del catálogo, con el detalle que se le agregó al error.
func TestUserErrorsKeepSpanishText(t *testing.T) {
	for _, known := range userErrors {
		msg := errorMsg(known.err)
		if got := msg.In(i18n.ES); got != known.err.Error() {
			t.Errorf("%s en español = %q, quería %q", known.key, got, known.err.Error())
		}
		if got := msg.In(i18n.EN); got == known.err.Error() || strings.Contains(got, "%!") {
			t.Errorf("%s en inglés = %q", known.key, got)
		}
	}
	wrapped := fmt.Errorf("%w (%d bytes)", invite.ErrKey, 31)
	if got := errorMsg(wrapped).In(i18n.ES); got != wrapped.Error() {
		t.Fatalf("envuelto en español = %q", got)
	}
	if got := errorMsg(wrapped).In(i18n.EN); got != "the invite has no valid key (31 bytes)" {
		t.Fatalf("envuelto en inglés = %q", got)
	}
	if _, err := invite.Parse("192.168.0.10:47830"); errorMsg(err).In(i18n.EN) != "it's not a VoxMesh invite" {
		t.Fatalf("invite.Parse: %q", errorMsg(err).In(i18n.EN))
	}
	// Un error técnico va tal cual en cualquier idioma.
	other := fmt.Errorf("abrir micrófono: %w", errors.New("dispositivo ocupado"))
	if got := errorMsg(other).In(i18n.EN); got != other.Error() {
		t.Fatalf("técnico = %q", got)
	}
}
