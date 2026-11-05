//go:build windows && cgo

package ui

import (
	"fmt"
	"math"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"voxmesh/internal/i18n"
	"voxmesh/internal/logging"
)

// Aviso de que comparto pantalla sobre el botón de VoxMesh en la barra de tareas: un punto rojo
// superpuesto (ITaskbarList3::SetOverlayIcon) que se ve aunque la ventana esté minimizada. Lo
// prende y apaga solo setSharingIndicator. Si algo falla, una línea de log y se sigue: compartir
// nunca depende de esto. Límites conocidos: si se reinicia el Explorador el botón se recrea sin el
// punto (vuelve con el próximo "Compartir"); con "botones pequeños de la barra de tareas" Windows
// no muestra superposiciones (queda el título).

var (
	taskbarUser32          = windows.NewLazySystemDLL("user32.dll")
	getSystemMetricsProc   = taskbarUser32.NewProc("GetSystemMetrics")
	createIconIndirectProc = taskbarUser32.NewProc("CreateIconIndirect")

	taskbarGdi32         = windows.NewLazySystemDLL("gdi32.dll")
	createDIBSectionProc = taskbarGdi32.NewProc("CreateDIBSection")
	createBitmapProc     = taskbarGdi32.NewProc("CreateBitmap")
	deleteObjectProc     = taskbarGdi32.NewProc("DeleteObject")

	coCreateInstance = ole32.NewProc("CoCreateInstance")

	// shobjidl.h
	clsidTaskbarList = windows.GUID{Data1: 0x56fdf344, Data2: 0xfd6d, Data3: 0x11d0, Data4: [8]byte{0x95, 0x8a, 0x00, 0x60, 0x97, 0xc9, 0xa0, 0x90}}
	iidTaskbarList3  = windows.GUID{Data1: 0xea1afb91, Data2: 0x9e28, Data3: 0x4b86, Data4: [8]byte{0x90, 0xe9, 0x9e, 0x9f, 0x8a, 0x5e, 0xef, 0xaf}}
)

const (
	clsctxInprocServer = 0x1
	smCxSmIcon         = 49 // ancho de un ícono chico (16 px a 96 DPI)
)

// taskbarUpdate es un pedido para el hilo del aviso.
type taskbarUpdate struct {
	hwnd uintptr
	on   bool
	text string // descripción del punto (la leen los lectores de pantalla)
}

// taskbarWorker es el hilo que habla con la barra de tareas. COM no corre en el hilo de UI: la
// llamada termina en el Explorador y no puede frenar la ventana. Se crea la primera vez que se
// comparte y vive lo que la app; toma solo el último pedido (canal de 1).
var taskbarWorker struct {
	once    sync.Once
	updates chan taskbarUpdate
}

// setTaskbarOverlay pide prender o apagar el punto. Hilo de UI (lee el HWND); nunca espera.
func (a *App) setTaskbarOverlay(on bool) {
	hwnd := a.dialogOwner()
	if hwnd == 0 {
		logging.Infof("Pantalla: sin aviso en la barra de tareas (no hay ventana)")
		return
	}
	taskbarWorker.once.Do(func() {
		taskbarWorker.updates = make(chan taskbarUpdate, 1)
		go logging.Guard("aviso en la barra de tareas", func() { runTaskbarOverlay(taskbarWorker.updates) })
	})
	// Un solo productor (el hilo de UI): después de sacar el pedido que no llegó a salir hay lugar,
	// aunque el hilo del aviso haya terminado.
	select {
	case <-taskbarWorker.updates:
	default:
	}
	taskbarWorker.updates <- taskbarUpdate{hwnd: hwnd, on: on, text: i18n.T("screen.taskbar_overlay")}
}

// runTaskbarOverlay es el hilo del aviso: COM (STA) propio y un ITaskbarList3 que dura lo que la
// app. Termina sin UnlockOSThread: un hilo con COM iniciado no vuelve al pool de Go (como el de
// selectFile).
func runTaskbarOverlay(updates <-chan taskbarUpdate) {
	runtime.LockOSThread()
	if hr, _, _ := coInitializeEx.Call(0, coinitApartment|coinitDisableOLE1); int32(hr) < 0 {
		logging.Infof("Pantalla: sin aviso en la barra de tareas: CoInitializeEx 0x%08x", uint32(hr))
		return
	}
	list, err := newTaskbarList()
	if err != nil {
		logging.Infof("Pantalla: sin aviso en la barra de tareas: %s", logging.FormatError(err))
		return
	}
	var dot uintptr // el ícono del punto: se dibuja una vez (el Explorador guarda su copia)
	for update := range updates {
		icon := uintptr(0)
		if update.on {
			if dot == 0 {
				if dot, err = sharingDotIcon(); err != nil {
					logging.Infof("Pantalla: sin aviso en la barra de tareas: %s", logging.FormatError(err))
					continue
				}
			}
			icon = dot
		}
		if err := list.setOverlayIcon(update.hwnd, icon, update.text); err != nil {
			logging.Infof("Pantalla: aviso en la barra de tareas (%s): %s", onOffText(update.on), logging.FormatError(err))
		}
	}
}

func onOffText(on bool) string {
	if on {
		return "prender"
	}
	return "apagar"
}

// taskbarList3 es un ITaskbarList3: el objeto COM empieza con el puntero a su tabla de métodos.
type taskbarList3 struct{ vtbl *taskbarList3Vtbl }

// taskbarList3Vtbl en el orden de shobjidl.h (IUnknown, ITaskbarList, ITaskbarList2,
// ITaskbarList3, hasta SetOverlayIcon). Se usan HrInit, SetOverlayIcon y Release.
type taskbarList3Vtbl struct {
	queryInterface, addRef, release                                 uintptr
	hrInit, addTab, deleteTab, activateTab, setActiveAlt            uintptr
	markFullscreenWindow                                            uintptr
	setProgressValue, setProgressState                              uintptr
	registerTab, unregisterTab, setTabOrder, setTabActive           uintptr
	thumbBarAddButtons, thumbBarUpdateButtons, thumbBarSetImageList uintptr
	setOverlayIcon                                                  uintptr
}

// newTaskbarList crea el ITaskbarList3 (en un hilo con COM iniciado).
func newTaskbarList() (*taskbarList3, error) {
	var list *taskbarList3
	hr, _, _ := coCreateInstance.Call(uintptr(unsafe.Pointer(&clsidTaskbarList)), 0, clsctxInprocServer,
		uintptr(unsafe.Pointer(&iidTaskbarList3)), uintptr(unsafe.Pointer(&list)))
	if int32(hr) < 0 || list == nil {
		return nil, fmt.Errorf("CoCreateInstance(TaskbarList) 0x%08x", uint32(hr))
	}
	if hr, _, _ := syscall.SyscallN(list.vtbl.hrInit, uintptr(unsafe.Pointer(list))); int32(hr) < 0 {
		list.close()
		return nil, fmt.Errorf("ITaskbarList::HrInit 0x%08x", uint32(hr))
	}
	return list, nil
}

func (l *taskbarList3) close() { syscall.SyscallN(l.vtbl.release, uintptr(unsafe.Pointer(l))) }

// setOverlayIcon pone icon sobre el botón de hwnd con la descripción text; icon 0 lo saca.
func (l *taskbarList3) setOverlayIcon(hwnd, icon uintptr, text string) error {
	var description *uint16
	if icon != 0 {
		var err error
		if description, err = windows.UTF16PtrFromString(text); err != nil {
			return err
		}
	}
	hr, _, _ := syscall.SyscallN(l.vtbl.setOverlayIcon, uintptr(unsafe.Pointer(l)), hwnd, icon, uintptr(unsafe.Pointer(description)))
	runtime.KeepAlive(description)
	if int32(hr) < 0 {
		return fmt.Errorf("SetOverlayIcon 0x%08x", uint32(hr))
	}
	return nil
}

type bitmapInfoHeader struct {
	size                         uint32
	width, height                int32
	planes, bitCount             uint16
	compression, sizeImage       uint32
	xPelsPerMeter, yPelsPerMeter int32
	clrUsed, clrImportant        uint32
}

type bitmapInfo struct {
	header bitmapInfoHeader
	colors [1]uint32 // sin uso con 32 bits
}

type iconInfo struct {
	icon               int32 // BOOL: ícono, no cursor
	xHotspot, yHotspot uint32
	mask, color        uintptr // HBITMAP
}

// sharingDotIcon arma el HICON del punto (sharingDotPixels) del tamaño de un ícono chico del
// sistema: color de 32 bits con alfa en una sección DIB + máscara vacía (manda el alfa).
func sharingDotIcon() (uintptr, error) {
	size := 16
	if metric, _, _ := getSystemMetricsProc.Call(smCxSmIcon); metric >= 16 && metric <= 64 {
		size = int(metric)
	}
	info := bitmapInfo{header: bitmapInfoHeader{width: int32(size), height: -int32(size), planes: 1, bitCount: 32}}
	info.header.size = uint32(unsafe.Sizeof(info.header))
	var bits uintptr
	color, _, callErr := createDIBSectionProc.Call(0, uintptr(unsafe.Pointer(&info)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if color == 0 || bits == 0 {
		return 0, fmt.Errorf("CreateDIBSection: %w", callErr)
	}
	defer deleteObjectProc.Call(color)
	copy(unsafe.Slice((*byte)(osPointer(bits)), size*size*4), sharingDotPixels(size))
	mask := make([]byte, (size+15)/16*2*size) // monocromo, filas de 16 bits
	maskBitmap, _, callErr := createBitmapProc.Call(uintptr(size), uintptr(size), 1, 1, uintptr(unsafe.Pointer(&mask[0])))
	if maskBitmap == 0 {
		return 0, fmt.Errorf("CreateBitmap: %w", callErr)
	}
	defer deleteObjectProc.Call(maskBitmap)
	icon := iconInfo{icon: 1, mask: maskBitmap, color: color} // CreateIconIndirect copia los bitmaps
	handle, _, callErr := createIconIndirectProc.Call(uintptr(unsafe.Pointer(&icon)))
	if handle == 0 {
		return 0, fmt.Errorf("CreateIconIndirect: %w", callErr)
	}
	return handle, nil
}

// sharingDotPixels es el punto en BGRA con alfa directo (sin premultiplicar), suavizado en el
// borde: rojo con un anillo blanco, para que se vea sobre la barra clara y sobre la oscura.
func sharingDotPixels(size int) []byte {
	pix := make([]byte, size*size*4)
	center := float64(size) / 2
	outer := center - 0.5                          // borde de afuera (blanco)
	inner := outer - math.Max(1, float64(size)/16) // borde del rojo
	for y := range size {
		for x := range size {
			d := math.Hypot(float64(x)+0.5-center, float64(y)+0.5-center)
			alpha := min(1, max(0, outer+0.5-d))
			if alpha == 0 {
				continue
			}
			red := min(1, max(0, inner+0.5-d)) / alpha // cuánto del píxel es rojo (el resto, blanco)
			o := (y*size + x) * 4
			pix[o] = uint8(255 - (255-40)*red + 0.5)    // B
			pix[o+1] = uint8(255 - (255-30)*red + 0.5)  // G
			pix[o+2] = uint8(255 - (255-220)*red + 0.5) // R
			pix[o+3] = uint8(255*alpha + 0.5)
		}
	}
	return pix
}
