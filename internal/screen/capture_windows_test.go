//go:build windows

package screen

import (
	"fmt"
	"image"
	"os"
	"runtime"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Estos tests usan la pantalla real: se saltean si no hay escritorio (servicio, CI sin sesión).

func openOrSkip(t *testing.T) *Capturer {
	t.Helper()
	c, err := OpenCapturer("")
	if err != nil {
		t.Skipf("no desktop to capture: %v", err)
	}
	return c
}

var procEnumDisplaySettingsW = user32.NewProc("EnumDisplaySettingsW")

// devMode es DEVMODEW (220 bytes) hasta los campos que importan acá.
type devMode struct {
	DeviceName    [32]uint16
	SpecVersion   uint16
	DriverVersion uint16
	Size          uint16
	DriverExtra   uint16
	Fields        uint32
	PositionX     int32
	PositionY     int32
	Orientation   uint32
	FixedOutput   uint32
	Color         int16
	Duplex        int16
	YResolution   int16
	TTOption      int16
	Collate       int16
	FormName      [32]uint16
	LogPixels     uint16
	BitsPerPel    uint32
	PelsWidth     uint32
	PelsHeight    uint32
	DisplayFlags  uint32
	Frequency     uint32
	rest          [8]uint32
}

// El modo de video de cada monitor está siempre en píxeles físicos. Si la captura no fuera DPI por
// monitor, con escalado de 125 % o 150 % los rectángulos saldrían más chicos (recortada o borrosa).
func TestMonitorsArePhysicalPixels(t *testing.T) {
	monitors, err := Monitors()
	if err != nil {
		t.Skipf("no monitors: %v", err)
	}
	for _, monitor := range monitors {
		mode := devMode{Size: uint16(unsafe.Sizeof(devMode{}))}
		name, _ := windows.UTF16PtrFromString(monitor.Device)
		const enumCurrentSettings = ^uintptr(0) // ENUM_CURRENT_SETTINGS = (DWORD)-1
		if ok, _, err := procEnumDisplaySettingsW.Call(uintptr(unsafe.Pointer(name)), enumCurrentSettings&0xFFFFFFFF, uintptr(unsafe.Pointer(&mode))); ok == 0 {
			t.Fatalf("EnumDisplaySettings(%s): %v", monitor.Device, err)
		}
		if monitor.Rect.Dx() != int(mode.PelsWidth) || monitor.Rect.Dy() != int(mode.PelsHeight) {
			t.Errorf("%s: monitor rect %v, video mode %d×%d", monitor.Device, monitor.Rect, mode.PelsWidth, mode.PelsHeight)
		}
		t.Logf("pantalla %d %s %v principal=%v", monitor.Number, monitor.Device, monitor.Rect, monitor.Primary)
	}
}

func TestCaptureRealScreen(t *testing.T) {
	c := openOrSkip(t)
	defer c.Close()
	if level := c.DPIAwareness(); level != 2 {
		t.Errorf("capture thread DPI awareness = %d, want 2 (per monitor)", level)
	}
	frame, err := c.Capture(nil)
	if err != nil {
		t.Skipf("capture failed (secure desktop?): %v", err)
	}
	m := c.Monitor()
	if frame.Rect.Dx() != m.Rect.Dx() || frame.Rect.Dy() != m.Rect.Dy() {
		t.Fatalf("frame %v, monitor %v", frame.Rect, m.Rect)
	}
	lit := 0
	for i := 0; i < len(frame.Pix); i += 4 {
		if frame.Pix[i+3] != 255 {
			t.Fatal("captured pixels must be opaque")
		}
		if frame.Pix[i]|frame.Pix[i+1]|frame.Pix[i+2] != 0 {
			lit++
		}
	}
	if lit == 0 {
		t.Fatal("the capture is all black")
	}
	// Un tile real pasa por PNG y vuelve idéntico.
	g := NewGrid(frame.Rect.Dx(), frame.Rect.Dy())
	r := g.Rect(g.Count() / 2)
	data, err := NewEncoder().Encode(frame, r)
	if err != nil {
		t.Fatal(err)
	}
	out := image.NewRGBA(frame.Rect)
	if err := DrawTile(out, r, data); err != nil {
		t.Fatal(err)
	}
	if !sameTile(out, frame, r) {
		t.Fatal("a real tile did not survive PNG unchanged")
	}
	// Una segunda captura reusa el buffer.
	again, err := c.Capture(frame)
	if err == nil && again != frame {
		t.Fatal("same size must reuse dst")
	}
}

// El cursor de verdad: se ve, tiene forma con alfa y la punta adentro.
func TestCursorTrackerRealCursor(t *testing.T) {
	tracker, err := OpenCursorTracker()
	if err != nil {
		t.Skipf("no desktop: %v", err)
	}
	defer tracker.Close()
	state := tracker.Poll()
	if !state.Visible {
		t.Skip("the cursor is hidden (touch screen, a game)")
	}
	if state.Shape == nil {
		t.Fatal("the system cursor must have a shape")
	}
	img := state.Shape.Image
	if img.Rect.Dx() > CursorMax || img.Rect.Dy() > CursorMax || !state.Shape.Hotspot.In(img.Rect.Inset(-1)) {
		t.Fatalf("shape %v hotspot %v", img.Rect, state.Shape.Hotspot)
	}
	opaque := 0
	for i := 3; i < len(img.Pix); i += 4 {
		if img.Pix[i] == 255 {
			opaque++
		}
	}
	if opaque == 0 {
		t.Fatal("the cursor shape has no opaque pixel")
	}
	again := tracker.Poll()
	if again.Shape != state.Shape && again.Visible && again.Shape != nil && again.Shape.ID == state.Shape.ID {
		t.Fatal("the same cursor must reuse its shape")
	}
	t.Logf("cursor en %v, forma %v (punta %v, %d píxeles opacos)", state.Pos, img.Rect.Size(), state.Shape.Hotspot, opaque)
}

// VOXMESH_SCREEN_BENCH=1 go test -run TestScreenBandwidth -v ./internal/screen/
// Mide con la pantalla real cuánto sale: imagen completa, pantalla quieta durante 20 s (con la
// imagen completa cada 10 s) y "mucho movimiento" simulado corriendo la captura 3 px por cuadro
// (cambian todos los tiles: cota superior). Para 64 y 128 de tile no hace falta tocar nada: se
// informa también el tamaño de la imagen completa con tiles de 128 (cuatro de 64 juntos).
func TestScreenBandwidth(t *testing.T) {
	if os.Getenv("VOXMESH_SCREEN_BENCH") == "" {
		t.Skip("set VOXMESH_SCREEN_BENCH=1")
	}
	c := openOrSkip(t)
	defer c.Close()
	captured, err := c.Capture(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, maxWidth := range []int{DefaultMaxWidth, 0} {
		frame := captured
		if maxWidth > 0 && captured.Rect.Dx() > maxWidth {
			w, h := FitSize(captured.Rect.Dx(), captured.Rect.Dy(), maxWidth, captured.Rect.Dy())
			frame = image.NewRGBA(image.Rect(0, 0, w, h))
			var s Scaler
			start := time.Now()
			s.Scale(frame, captured)
			t.Logf("achicar %v → %v: %v", captured.Rect.Size(), frame.Rect.Size(), time.Since(start))
		}
		rgb, rgbTime := fullImageBytes(t, frame, TileSize, true)
		full, encodeTime := fullImageBytes(t, frame, TileSize, false)
		big, _ := fullImageBytes(t, frame, 2*TileSize, true)
		t.Logf("%dx%d: imagen completa %d KB con paleta/lisos (%v), %d KB todo PNG RGB (%v, %.0f%% más), %d KB RGB con tiles de 128",
			frame.Rect.Dx(), frame.Rect.Dy(), full/1024, encodeTime, rgb/1024, rgbTime, 100*float64(rgb-full)/float64(full), big/1024)
		t.Logf("  imagen completa: %.1f s a 1,5 Mbps (internet), %.2f s a 20 Mbps (LAN)", float64(full)/187_500, float64(full)/2_500_000)
		moving := scrolled(frame, 3)
		movingBytes, _ := fullImageBytes(t, moving, TileSize, false)
		t.Logf("  todo cambia en cada captura: %d KB por cuadro, %s a 2 capturas/s (el presupuesto la corta)",
			movingBytes/1024, kbps(2*movingBytes, time.Second))
		if maxWidth > 0 && captured.Rect.Dx() <= maxWidth {
			break // el monitor ya entra: la resolución completa da lo mismo
		}
	}
}

// fullImageBytes son los bytes en la red (chunks con cabecera + cifrado + UDP/IP) de mandar todos
// los tiles de lado tile: con Encode (paleta/lisos) o, con rgb, todo en PNG RGB.
func fullImageBytes(t *testing.T, frame *image.RGBA, tile int, rgb bool) (int, time.Duration) {
	t.Helper()
	enc := NewEncoder()
	total := 0
	var encodeTime time.Duration
	for y := 0; y < frame.Rect.Dy(); y += tile {
		for x := 0; x < frame.Rect.Dx(); x += tile {
			r := image.Rect(x, y, min(x+tile, frame.Rect.Dx()), min(y+tile, frame.Rect.Dy()))
			start := time.Now()
			size := 0
			if rgb {
				enc.out.Reset()
				if err := enc.png.Encode(&enc.out, frame.SubImage(r)); err != nil {
					t.Fatal(err)
				}
				size = enc.out.Len()
			} else {
				data, err := enc.Encode(frame, r)
				if err != nil {
					t.Fatal(err)
				}
				size = len(data)
			}
			encodeTime += time.Since(start)
			total += size + ChunkCount(size)*(HeaderSize+16+28+28) // cabecera de tile, VM02, GCM, UDP/IP
		}
	}
	return total, encodeTime
}

func scrolled(frame *image.RGBA, dy int) *image.RGBA {
	out := image.NewRGBA(frame.Rect)
	copy(out.Pix, frame.Pix[dy*frame.Stride:])
	return out
}

func kbps(bytes int, per time.Duration) string {
	return fmt.Sprintf("%.0f kbps", float64(bytes)*8/per.Seconds()/1000)
}

func BenchmarkCapture(b *testing.B) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	c, err := OpenCapturer("")
	if err != nil {
		b.Skip(err)
	}
	defer c.Close()
	var frame *image.RGBA
	for b.Loop() {
		if frame, err = c.Capture(frame); err != nil {
			b.Fatal(err)
		}
	}
}
