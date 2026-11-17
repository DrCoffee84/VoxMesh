package audio

import (
	"testing"
	"time"
)

func TestDeviceHealth(t *testing.T) {
	now := time.Unix(1000, 0)
	at := func(ago time.Duration) int64 { return now.Add(-ago).UnixNano() }
	cases := []struct {
		name              string
		started           int64
		input             bool
		capture, playback int64
		lost              string
		wantOK            bool
	}{
		{"recién abierto sin callbacks", at(time.Second), true, 0, 0, "", true},
		{"los dos vivos", at(time.Minute), true, at(10 * time.Millisecond), at(10 * time.Millisecond), "", true},
		{"captura muda", at(time.Minute), true, at(5 * time.Second), at(10 * time.Millisecond), "", false},
		{"salida muda", at(time.Minute), true, at(10 * time.Millisecond), at(5 * time.Second), "", false},
		{"nunca arrancó", at(time.Minute), true, 0, 0, "", false},
		{"Stop del sistema", at(time.Minute), true, at(10 * time.Millisecond), at(10 * time.Millisecond), "micrófono", false},
		{"Stop recién abierto", at(time.Second), true, 0, 0, "salida", false},
		// Sin micrófono (no abrió, o el celular de entrada): solo cuenta la salida (R11).
		{"sin micrófono, salida viva", at(time.Minute), false, 0, at(10 * time.Millisecond), "", true},
		{"sin micrófono, salida muda", at(time.Minute), false, 0, at(5 * time.Second), "", false},
		{"sin micrófono, salida nunca arrancó", at(time.Minute), false, 0, 0, "", false},
		{"sin micrófono, Stop de la salida", at(time.Minute), false, 0, at(10 * time.Millisecond), "salida", false},
	}
	for _, c := range cases {
		ok, reason := deviceHealth(now, c.started, c.input, c.capture, c.playback, c.lost)
		if ok != c.wantOK {
			t.Errorf("%s: ok=%v (%q), quería %v", c.name, ok, reason, c.wantOK)
		}
		if !ok && reason == "" {
			t.Errorf("%s: sin motivo", c.name)
		}
	}
}
