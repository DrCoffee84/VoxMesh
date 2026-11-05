//go:build windows

package screen

import (
	"cmp"
	"errors"
	"fmt"
	"image"
	"runtime"
	"slices"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Captura con GDI: BitBlt del DC de la pantalla a una sección DIB (32 bits, de arriba abajo) y una
// copia de sus píxeles a un buffer de Go. El cursor no va en la captura: lo sigue CursorTracker
// (cursor_windows.go) y lo dibuja el visor. A 1–2 cuadros por
// segundo alcanza y no necesita dependencias. No GetDIBits: en una PC con Windows 10 devolvía 0
// líneas con un destino de más de ~1 MB (1920×256 ya falla), y la sección DIB además se ahorra
// esa copia en el kernel.
// Límites: lo que Windows dibuja fuera de GDI sale negro (video con DRM, algunos juegos en pantalla
// completa) y el escritorio seguro (UAC, Ctrl+Alt+Supr, bloqueo) no se puede capturar: BitBlt
// falla y queda la última imagen.

var (
	user32 = windows.NewLazySystemDLL("user32.dll")
	gdi32  = windows.NewLazySystemDLL("gdi32.dll")

	procEnumDisplayMonitors                 = user32.NewProc("EnumDisplayMonitors")
	procGetMonitorInfoW                     = user32.NewProc("GetMonitorInfoW")
	procGetDC                               = user32.NewProc("GetDC")
	procReleaseDC                           = user32.NewProc("ReleaseDC")
	procGetCursorInfo                       = user32.NewProc("GetCursorInfo")
	procGetIconInfo                         = user32.NewProc("GetIconInfo")
	procDrawIconEx                          = user32.NewProc("DrawIconEx")
	procSetThreadDpiAwarenessContext        = user32.NewProc("SetThreadDpiAwarenessContext")
	procGetThreadDpiAwarenessContext        = user32.NewProc("GetThreadDpiAwarenessContext")
	procGetAwarenessFromDpiAwarenessContext = user32.NewProc("GetAwarenessFromDpiAwarenessContext")

	procCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	procCreateDIBSection   = gdi32.NewProc("CreateDIBSection")
	procSelectObject       = gdi32.NewProc("SelectObject")
	procDeleteObject       = gdi32.NewProc("DeleteObject")
	procDeleteDC           = gdi32.NewProc("DeleteDC")
	procBitBlt             = gdi32.NewProc("BitBlt")
	procGetObjectW         = gdi32.NewProc("GetObjectW")
	procGdiFlush           = gdi32.NewProc("GdiFlush")
)

// Constantes de wingdi.h y winuser.h.
const (
	srcCopy             = 0x00CC0020
	captureBlt          = 0x40000000 // incluye ventanas en capas (menús, tooltips)
	dibRGBColors        = 0
	biRGB               = 0
	cursorShowing       = 0x1
	diNormal            = 0x3
	monitorInfoPrimary  = 0x1
	ccDeviceName        = 32
	dpiPerMonitorAware2 = ^uintptr(3) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 = (HANDLE)-4
)

type winRect struct{ Left, Top, Right, Bottom int32 }

type monitorInfoEx struct {
	Size    uint32
	Monitor winRect
	Work    winRect
	Flags   uint32
	Device  [ccDeviceName]uint16
}

type cursorInfo struct {
	Size   uint32
	Flags  uint32
	Cursor uintptr
	X, Y   int32
}

type iconInfo struct {
	Icon               int32
	XHotspot, YHotspot uint32
	Mask, Color        uintptr
}

type bitmapInfo struct {
	Size          uint32
	Width, Height int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
	colors        [4]uint32 // RGBQUAD[]: con BI_RGB y 32 bits no se usa; margen
}

// perMonitorThread pone el hilo actual (bloqueado con LockOSThread) en DPI por monitor v2 y
// devuelve cómo dejarlo como estaba. Solo este hilo: Fyne no se entera. GLFW ya pone así a todo el
// proceso al iniciar, pero los tests no tienen Fyne y una versión vieja de Windows tampoco.
func perMonitorThread() func() {
	if procSetThreadDpiAwarenessContext.Find() != nil {
		return func() {}
	}
	previous, _, _ := procSetThreadDpiAwarenessContext.Call(dpiPerMonitorAware2)
	return func() {
		if previous != 0 {
			_, _, _ = procSetThreadDpiAwarenessContext.Call(previous)
		}
	}
}

// threadDPIAwareness es el nivel de DPI del hilo actual: 0 sin DPI, 1 de sistema, 2 por monitor,
// -1 si Windows no lo informa.
func threadDPIAwareness() int {
	if procGetThreadDpiAwarenessContext.Find() != nil || procGetAwarenessFromDpiAwarenessContext.Find() != nil {
		return -1
	}
	context, _, _ := procGetThreadDpiAwarenessContext.Call()
	awareness, _, _ := procGetAwarenessFromDpiAwarenessContext.Call(context)
	return int(int32(awareness))
}

// Monitors lista las pantallas en píxeles físicos, de izquierda a derecha.
func Monitors() ([]Monitor, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	restore := perMonitorThread()
	defer restore()
	return enumMonitors()
}

var (
	enumMu       sync.Mutex // EnumDisplayMonitors llama al callback en el mismo hilo, antes de volver
	enumFound    []Monitor
	enumCallback = sync.OnceValue(func() uintptr { return windows.NewCallback(monitorEnumProc) })
)

func monitorEnumProc(monitor, _, _, _ uintptr) uintptr {
	info := monitorInfoEx{Size: uint32(unsafe.Sizeof(monitorInfoEx{}))}
	if ok, _, _ := procGetMonitorInfoW.Call(monitor, uintptr(unsafe.Pointer(&info))); ok != 0 {
		enumFound = append(enumFound, Monitor{
			Device:  windows.UTF16ToString(info.Device[:]),
			Rect:    image.Rect(int(info.Monitor.Left), int(info.Monitor.Top), int(info.Monitor.Right), int(info.Monitor.Bottom)),
			Primary: info.Flags&monitorInfoPrimary != 0,
		})
	}
	return 1 // seguir
}

// enumMonitors lista los monitores con el DPI que tenga el hilo actual.
func enumMonitors() ([]Monitor, error) {
	enumMu.Lock()
	defer enumMu.Unlock()
	enumFound = enumFound[:0]
	if ok, _, err := procEnumDisplayMonitors.Call(0, 0, enumCallback(), 0); ok == 0 {
		return nil, fmt.Errorf("EnumDisplayMonitors: %w", err)
	}
	monitors := slices.Clone(enumFound)
	slices.SortFunc(monitors, func(a, b Monitor) int {
		return cmp.Or(cmp.Compare(a.Rect.Min.X, b.Rect.Min.X), cmp.Compare(a.Rect.Min.Y, b.Rect.Min.Y))
	})
	for i := range monitors {
		monitors[i].Number = i + 1
	}
	if len(monitors) == 0 {
		return nil, errors.New("no hay monitores")
	}
	return monitors, nil
}

// Capturer captura un monitor. Bloquea su goroutine en el hilo del sistema desde OpenCapturer hasta
// Close (los DC de GDI y el DPI por monitor son de ese hilo): se usa siempre desde la misma
// goroutine.
type Capturer struct {
	device   string // el elegido; si desaparece se usa el principal
	monitor  Monitor
	restore  func()
	screenDC uintptr
	memDC    uintptr
	bitmap   uintptr
	bits     uintptr // píxeles de la sección DIB (memoria de Windows, BGRA de arriba abajo)
	stock    uintptr // el bitmap que traía memDC: vuelve antes de borrar el nuestro
	w, h     int
}

// OpenCapturer prepara la captura del monitor device ("" = el principal).
func OpenCapturer(device string) (*Capturer, error) {
	runtime.LockOSThread()
	c := &Capturer{device: device, restore: perMonitorThread()}
	dc, _, err := procGetDC.Call(0)
	if dc == 0 {
		c.Close()
		return nil, fmt.Errorf("GetDC: %w", err)
	}
	c.screenDC = dc
	mem, _, err := procCreateCompatibleDC.Call(dc)
	if mem == 0 {
		c.Close()
		return nil, fmt.Errorf("CreateCompatibleDC: %w", err)
	}
	c.memDC = mem
	if err := c.resolve(); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// Monitor es el monitor de la última captura.
func (c *Capturer) Monitor() Monitor { return c.monitor }

// DPIAwareness es el nivel de DPI del hilo de captura (2 = por monitor: píxeles físicos).
func (c *Capturer) DPIAwareness() int { return threadDPIAwareness() }

// resolve busca el monitor elegido (puede haber cambiado de tamaño o desaparecido).
func (c *Capturer) resolve() error {
	monitors, err := enumMonitors()
	if err != nil {
		return err
	}
	c.monitor = PickMonitor(monitors, c.device)
	return nil
}

// Capture captura el monitor en dst (o en uno nuevo si cambió el tamaño), en RGBA opaco.
func (c *Capturer) Capture(dst *image.RGBA) (*image.RGBA, error) {
	if err := c.resolve(); err != nil {
		return nil, err
	}
	r := c.monitor.Rect
	w, h := r.Dx(), r.Dy()
	if err := c.ensureBitmap(w, h); err != nil {
		return nil, err
	}
	if ok, _, err := procBitBlt.Call(c.memDC, 0, 0, uintptr(w), uintptr(h), c.screenDC,
		uintptr(int32(r.Min.X)), uintptr(int32(r.Min.Y)), srcCopy|captureBlt); ok == 0 {
		return nil, fmt.Errorf("BitBlt: %w", err)
	}
	_, _, _ = procGdiFlush.Call() // lo encolado por GDI tiene que estar en los bits antes de leerlos
	if dst == nil || dst.Rect.Dx() != w || dst.Rect.Dy() != h {
		dst = image.NewRGBA(image.Rect(0, 0, w, h))
	}
	copy(dst.Pix, unsafe.Slice((*byte)(osPointer(c.bits)), w*h*4))
	bgraToRGBA(dst.Pix)
	return dst, nil
}

func (c *Capturer) ensureBitmap(w, h int) error {
	if c.bitmap != 0 && c.w == w && c.h == h {
		return nil
	}
	c.dropBitmap()
	info := bitmapInfo{Width: int32(w), Height: -int32(h), Planes: 1, BitCount: 32, Compression: biRGB}
	info.Size = uint32(unsafe.Offsetof(info.colors)) // BITMAPINFOHEADER: 40 bytes
	var bits uintptr
	bitmap, _, err := procCreateDIBSection.Call(c.screenDC, uintptr(unsafe.Pointer(&info)), dibRGBColors,
		uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bitmap == 0 || bits == 0 {
		return fmt.Errorf("CreateDIBSection %d×%d: %w", w, h, err)
	}
	c.bitmap, c.bits, c.w, c.h = bitmap, bits, w, h
	c.stock, _, _ = procSelectObject.Call(c.memDC, bitmap)
	return nil
}

func (c *Capturer) dropBitmap() {
	if c.bitmap == 0 {
		return
	}
	_, _, _ = procSelectObject.Call(c.memDC, c.stock)
	_, _, _ = procDeleteObject.Call(c.bitmap)
	c.bitmap, c.bits = 0, 0
}

// osPointer convierte una dirección que devolvió Windows (los bits de la sección DIB: memoria fuera
// del heap de Go) en unsafe.Pointer. Es la misma conversión que unsafe.Pointer(addr); leerla a
// través de &addr evita el aviso de go vet, que no puede saber que no es un puntero de Go (igual
// que en ui/file_windows.go).
func osPointer(addr uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&addr))
}

// Close libera GDI, devuelve el DPI del hilo y suelta el hilo.
func (c *Capturer) Close() {
	c.dropBitmap()
	if c.memDC != 0 {
		_, _, _ = procDeleteDC.Call(c.memDC)
		c.memDC = 0
	}
	if c.screenDC != 0 {
		_, _, _ = procReleaseDC.Call(0, c.screenDC)
		c.screenDC = 0
	}
	if c.restore != nil {
		c.restore()
		c.restore = nil
		runtime.UnlockOSThread()
	}
}
