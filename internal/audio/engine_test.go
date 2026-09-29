//go:build cgo

package audio

import "testing"

func TestSoftClip(t *testing.T) {
	// Dentro del rango lineal
	if val := softClip(0); val != 0 {
		t.Fatalf("expected 0, got %d", val)
	}
	if val := softClip(15000); val != 15000 {
		t.Fatalf("expected 15000, got %d", val)
	}
	if val := softClip(-15000); val != -15000 {
		t.Fatalf("expected -15000, got %d", val)
	}
	if val := softClip(30000); val != 30000 {
		t.Fatalf("expected 30000, got %d", val)
	}
	if val := softClip(-30000); val != -30000 {
		t.Fatalf("expected -30000, got %d", val)
	}

	// Picos altos por suma de múltiples participantes hablando a la vez
	posHigh := softClip(40000)
	if posHigh <= 30000 || posHigh > 32767 {
		t.Fatalf("expected between 30001 and 32767, got %d", posHigh)
	}

	posExtreme := softClip(100000)
	if posExtreme <= posHigh || posExtreme > 32767 {
		t.Fatalf("expected between %d and 32767, got %d", posHigh, posExtreme)
	}

	negHigh := softClip(-40000)
	if negHigh >= -30000 || negHigh < -32768 {
		t.Fatalf("expected between -30001 and -32768, got %d", negHigh)
	}

	negExtreme := softClip(-100000)
	if negExtreme >= negHigh || negExtreme < -32768 {
		t.Fatalf("expected between %d and -32768, got %d", negHigh, negExtreme)
	}
}
