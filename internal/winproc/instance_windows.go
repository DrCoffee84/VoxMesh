//go:build windows

package winproc

import (
	"fmt"
	"hash/fnv"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

var procAllowSetForegroundWindow = user32.NewProc("AllowSetForegroundWindow")

// asfwAny deja que cualquier proceso pase su ventana adelante (ASFW_ANY, winuser.h).
const asfwAny = 0xFFFFFFFF

// Instance marca que VoxMesh está abierto desde una carpeta (AGENDA C21): un mutex con nombre (lo
// que importa es que exista: Windows lo suelta cuando el proceso termina, aunque se cuelgue o lo
// maten) y un evento con el que otra instancia le pide que muestre su ventana. Los dos son locales a
// la sesión de Windows y no llevan datos: lo peor que puede hacer otro programa es mostrar la
// ventana.
type Instance struct {
	mutex windows.Handle // no se cierra nunca: Windows lo suelta al terminar el proceso
	show  windows.Handle // evento de reinicio automático; 0 si no se pudo crear
}

// instanceName es el nombre de los objetos de una instalación: uno por carpeta del exe, porque
// voxmesh.json vive al lado del exe (config.Path). Dos copias en carpetas distintas (así se prueban
// varias instancias en una PC) son dos VoxMesh distintos.
func instanceName(exe string) string {
	dir, err := filepath.Abs(filepath.Dir(exe))
	if err != nil {
		dir = filepath.Dir(exe)
	}
	hash := fnv.New64a()
	hash.Write([]byte(strings.ToLower(filepath.Clean(dir))))
	return fmt.Sprintf(`Local\VoxMesh-%016x`, hash.Sum64())
}

// SingleInstance se fija si ya hay un VoxMesh abierto desde la carpeta de exe. first = false: ya
// había uno (la nueva le pide que se muestre con RequestShow y se va). Si Windows no deja crear los
// objetos, sigue como si fuera la primera: mejor dos instancias que ninguna.
func SingleInstance(exe string) (inst *Instance, first bool) {
	name := instanceName(exe)
	inst = &Instance{}
	mutexName, _ := windows.UTF16PtrFromString(name)
	mutex, err := windows.CreateMutex(nil, false, mutexName)
	inst.mutex = mutex
	first = err != windows.ERROR_ALREADY_EXISTS
	eventName, _ := windows.UTF16PtrFromString(name + "-show")
	if event, err := windows.CreateEvent(nil, 0, 0, eventName); event != 0 && (err == nil || err == windows.ERROR_ALREADY_EXISTS) {
		inst.show = event
	}
	return inst, first
}

// RequestShow le pide a la instancia que ya estaba abierta que muestre su ventana. La deja pasar
// adelante: Windows solo se lo permite al proceso que recibió el último clic (este, que abrió el
// usuario).
func (i *Instance) RequestShow() {
	if i.show == 0 {
		return
	}
	_, _, _ = procAllowSetForegroundWindow.Call(asfwAny)
	_ = windows.SetEvent(i.show)
}

// OnShowRequest llama a fn (en otra goroutine) cada vez que otra instancia pide mostrar la
// ventana. Ocupa un hilo esperando el evento mientras dure el proceso.
func (i *Instance) OnShowRequest(fn func()) {
	if i.show == 0 {
		return
	}
	go func() {
		for {
			if event, err := windows.WaitForSingleObject(i.show, windows.INFINITE); err != nil || event != windows.WAIT_OBJECT_0 {
				return
			}
			fn()
		}
	}()
}
