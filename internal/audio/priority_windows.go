//go:build windows

package audio

import (
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	avrt                              = windows.NewLazySystemDLL("avrt.dll")
	procAvSetMmThreadCharacteristicsW = avrt.NewProc("AvSetMmThreadCharacteristicsW")
	kernel32                          = windows.NewLazySystemDLL("kernel32.dll")
	procSetThreadPriority             = kernel32.NewProc("SetThreadPriority")
)

// threadPriorityHighest es THREAD_PRIORITY_HIGHEST de Win32.
const threadPriorityHighest = 2

// LockAudioThread fija la goroutine actual a un hilo del sistema y le da prioridad de audio: lo
// registra en MMCSS como "Pro Audio" (lo que usan las apps de audio en Windows) o, si no se puede,
// prioridad alta. Así una PC cargada (un juego) no deja sin CPU al procesamiento de la voz y no se
// pierden frames. El hilo nunca se suelta: muere con la goroutine, así no queda un hilo con
// prioridad alta en el pool de Go. Devuelve qué consiguió, para el log.
func LockAudioThread() string {
	runtime.LockOSThread()
	if procAvSetMmThreadCharacteristicsW.Find() == nil {
		if task, err := windows.UTF16PtrFromString("Pro Audio"); err == nil {
			var index uint32
			handle, _, _ := procAvSetMmThreadCharacteristicsW.Call(uintptr(unsafe.Pointer(task)), uintptr(unsafe.Pointer(&index)))
			if handle != 0 {
				return "MMCSS Pro Audio"
			}
		}
	}
	if ok, _, err := procSetThreadPriority.Call(uintptr(windows.CurrentThread()), threadPriorityHighest); ok == 0 {
		return "prioridad normal (" + err.Error() + ")"
	}
	return "prioridad alta"
}
