//go:build windows

package screen

import (
	"fmt"
	"image"
	"runtime"
	"unsafe"
)

// CursorTracker sigue el cursor del sistema: dónde está (GetCursorInfo) y qué forma tiene (la dibuja
// con DrawIconEx sobre negro y sobre blanco: CursorFromRenders). Como Capturer, bloquea su goroutine
// en el hilo del sistema con DPI por monitor desde OpenCursorTracker hasta Close: se usa siempre
// desde la misma goroutine. Corre aparte de la captura, así el cursor no espera a los tiles.
type CursorTracker struct {
	restore  func()
	screenDC uintptr
	memDC    uintptr
	shapes   map[uintptr]*CursorShape // por HCURSOR (nil = no se pudo dibujar)
}

// cursorRenderMax acota el tamaño al que se dibuja un cursor (con la accesibilidad de Windows
// pueden ser enormes); después CursorFromRenders lo achica a CursorMax.
const cursorRenderMax = 256

// bitmapObject es BITMAP (GetObjectW).
type bitmapObject struct {
	Type, Width, Height, WidthBytes int32
	Planes, BitsPixel               uint16
	Bits                            uintptr
}

func OpenCursorTracker() (*CursorTracker, error) {
	runtime.LockOSThread()
	t := &CursorTracker{restore: perMonitorThread(), shapes: make(map[uintptr]*CursorShape)}
	dc, _, err := procGetDC.Call(0)
	if dc == 0 {
		t.Close()
		return nil, fmt.Errorf("GetDC: %w", err)
	}
	t.screenDC = dc
	mem, _, err := procCreateCompatibleDC.Call(dc)
	if mem == 0 {
		t.Close()
		return nil, fmt.Errorf("CreateCompatibleDC: %w", err)
	}
	t.memDC = mem
	return t, nil
}

// Poll mira el cursor ahora. La forma se dibuja una vez por HCURSOR.
func (t *CursorTracker) Poll() CursorState {
	info := cursorInfo{Size: uint32(unsafe.Sizeof(cursorInfo{}))}
	if ok, _, _ := procGetCursorInfo.Call(uintptr(unsafe.Pointer(&info))); ok == 0 {
		return CursorState{}
	}
	state := CursorState{Visible: info.Flags&cursorShowing != 0 && info.Cursor != 0, Pos: image.Pt(int(info.X), int(info.Y))}
	if !state.Visible {
		return state
	}
	shape, known := t.shapes[info.Cursor]
	if !known {
		shape = t.render(info.Cursor)
		if len(t.shapes) >= 64 {
			clear(t.shapes)
		}
		t.shapes[info.Cursor] = shape
	}
	state.Shape = shape
	return state
}

// render dibuja el cursor sobre negro y sobre blanco y arma la forma con alfa.
func (t *CursorTracker) render(cursor uintptr) *CursorShape {
	var icon iconInfo
	if ok, _, _ := procGetIconInfo.Call(cursor, uintptr(unsafe.Pointer(&icon))); ok == 0 {
		return nil
	}
	// GetIconInfo crea copias de los bitmaps: son nuestras.
	defer func() {
		if icon.Mask != 0 {
			_, _, _ = procDeleteObject.Call(icon.Mask)
		}
		if icon.Color != 0 {
			_, _, _ = procDeleteObject.Call(icon.Color)
		}
	}()
	var bm bitmapObject
	w, h := 0, 0
	if icon.Color != 0 {
		if n, _, _ := procGetObjectW.Call(icon.Color, unsafe.Sizeof(bm), uintptr(unsafe.Pointer(&bm))); n != 0 {
			w, h = int(bm.Width), int(bm.Height)
		}
	} else if n, _, _ := procGetObjectW.Call(icon.Mask, unsafe.Sizeof(bm), uintptr(unsafe.Pointer(&bm))); n != 0 {
		w, h = int(bm.Width), int(bm.Height)/2 // monocromo: máscara AND arriba, XOR abajo
	}
	if w <= 0 || h <= 0 || w > cursorRenderMax || h > cursorRenderMax {
		return nil
	}
	onBlack, err := t.renderOn(cursor, w, h, 0x00)
	if err != nil {
		return nil
	}
	onWhite, err := t.renderOn(cursor, w, h, 0xff)
	if err != nil {
		return nil
	}
	shape := CursorFromRenders(onBlack, onWhite, image.Pt(int(icon.XHotspot), int(icon.YHotspot)))
	return &shape
}

// renderOn dibuja el cursor en una sección DIB de w×h llena con background.
func (t *CursorTracker) renderOn(cursor uintptr, w, h int, background byte) (*image.RGBA, error) {
	info := bitmapInfo{Width: int32(w), Height: -int32(h), Planes: 1, BitCount: 32, Compression: biRGB}
	info.Size = uint32(unsafe.Offsetof(info.colors))
	var bits uintptr
	bitmap, _, err := procCreateDIBSection.Call(t.screenDC, uintptr(unsafe.Pointer(&info)), dibRGBColors, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bitmap == 0 || bits == 0 {
		return nil, fmt.Errorf("CreateDIBSection: %w", err)
	}
	defer procDeleteObject.Call(bitmap)
	previous, _, _ := procSelectObject.Call(t.memDC, bitmap)
	defer procSelectObject.Call(t.memDC, previous)
	pix := unsafe.Slice((*byte)(osPointer(bits)), w*h*4)
	for i := range pix {
		pix[i] = background
	}
	if ok, _, err := procDrawIconEx.Call(t.memDC, 0, 0, cursor, uintptr(w), uintptr(h), 0, 0, diNormal); ok == 0 {
		return nil, fmt.Errorf("DrawIconEx: %w", err)
	}
	_, _, _ = procGdiFlush.Call()
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	copy(out.Pix, pix)
	bgraToRGBA(out.Pix)
	return out, nil
}

// Close libera GDI, devuelve el DPI del hilo y suelta el hilo.
func (t *CursorTracker) Close() {
	if t.memDC != 0 {
		_, _, _ = procDeleteDC.Call(t.memDC)
		t.memDC = 0
	}
	if t.screenDC != 0 {
		_, _, _ = procReleaseDC.Call(0, t.screenDC)
		t.screenDC = 0
	}
	if t.restore != nil {
		t.restore()
		t.restore = nil
		runtime.UnlockOSThread()
	}
}
