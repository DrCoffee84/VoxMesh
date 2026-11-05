//go:build windows && cgo

package ui

import (
	"runtime"
	"testing"
)

// El punto: rojo opaco en el centro, transparente en las esquinas, borde blanco suavizado.
func TestSharingDotPixels(t *testing.T) {
	for _, size := range []int{16, 20, 24, 32} {
		pix := sharingDotPixels(size)
		at := func(x, y int) (b, g, r, a uint8) {
			o := (y*size + x) * 4
			return pix[o], pix[o+1], pix[o+2], pix[o+3]
		}
		if b, g, r, a := at(size/2, size/2); a != 255 || r != 220 || g != 30 || b != 40 {
			t.Fatalf("%d px: center = %d,%d,%d α%d, want opaque red", size, r, g, b, a)
		}
		if _, _, _, a := at(0, 0); a != 0 {
			t.Fatalf("%d px: corner α%d, want transparent", size, a)
		}
		if b, g, r, a := at(size/2, 0); a == 0 || a == 255 || r != 255 || g != 255 || b != 255 {
			t.Fatalf("%d px: top edge = %d,%d,%d α%d, want white, half transparent", size, r, g, b, a)
		}
	}
}

// El ícono se arma con GDI de verdad (estructuras de Win32 bien armadas).
func TestSharingDotIcon(t *testing.T) {
	icon, err := sharingDotIcon()
	if err != nil {
		t.Fatal(err)
	}
	if ok, _, _ := taskbarUser32.NewProc("DestroyIcon").Call(icon); ok == 0 {
		t.Fatal("DestroyIcon failed")
	}
}

// ITaskbarList3 se crea e inicia (HrInit es el método 3 de la tabla). Sin Explorador (un servicio,
// una sesión sin escritorio) no se puede: se saltea, igual que la app sigue sin el punto.
func TestTaskbarListCreates(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if hr, _, _ := coInitializeEx.Call(0, coinitApartment|coinitDisableOLE1); int32(hr) < 0 {
		t.Skipf("CoInitializeEx 0x%08x", uint32(hr))
	}
	defer coUninitialize.Call()
	list, err := newTaskbarList()
	if err != nil {
		t.Skip(err)
	}
	list.close()
}
