//go:build windows

// Package winproc le pide a Windows que trate a VoxMesh como una app de comunicación que tiene que
// estar siempre activa: que no la frene en segundo plano, que no la reemplace por la ventana
// fantasma de "No responde" y que no suspenda la PC en medio de una sala.
package winproc

import (
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32                          = windows.NewLazySystemDLL("kernel32.dll")
	user32                            = windows.NewLazySystemDLL("user32.dll")
	procSetProcessInformation         = kernel32.NewProc("SetProcessInformation")
	procPowerCreateRequest            = kernel32.NewProc("PowerCreateRequest")
	procPowerSetRequest               = kernel32.NewProc("PowerSetRequest")
	procPowerClearRequest             = kernel32.NewProc("PowerClearRequest")
	procDisableProcessWindowsGhosting = user32.NewProc("DisableProcessWindowsGhosting")
)

// Constantes de processthreadsapi.h y winnt.h.
const (
	processPowerThrottling          = 4 // PROCESS_INFORMATION_CLASS
	powerThrottlingCurrentVersion   = 1
	powerThrottlingExecutionSpeed   = 0x1
	powerThrottlingIgnoreTimerRes   = 0x4
	powerRequestContextVersion      = 0
	powerRequestContextSimpleString = 0x1
	powerRequestSystemRequired      = 1 // POWER_REQUEST_TYPE
)

type powerThrottlingState struct {
	Version     uint32
	ControlMask uint32
	StateMask   uint32
}

// reasonContext es REASON_CONTEXT con la variante SimpleReasonString de la unión (que en x64
// mide lo que la variante Detailed: 24 bytes).
type reasonContext struct {
	Version            uint32
	Flags              uint32
	SimpleReasonString *uint16
	_                  [2]uintptr
}

// Configure ajusta el proceso una sola vez, al arrancar (antes de crear ventanas). Cada paso es
// best effort: lo que no se pudo queda dicho en el texto que devuelve, para el log.
//   - Prioridad "por encima de lo normal": con un juego abierto, la red, la UI y el failover le
//     ganan a los procesos normales (el envío de audio además usa MMCSS). HIGH no: compite con
//     el propio sistema.
//   - Fuera del power throttling (EcoQoS / modo eficiencia): minimizada o en segundo plano,
//     Windows le baja la frecuencia y el scheduling y el audio se atrasa.
//   - Sin ghosting: si la UI tarda más de 5 s, Windows no cambia la ventana por la fantasma de
//     "No responde" (en la barra de tareas parecía que la app se había reiniciado).
func Configure() string {
	var done []string
	if err := windows.SetPriorityClass(windows.CurrentProcess(), windows.ABOVE_NORMAL_PRIORITY_CLASS); err != nil {
		done = append(done, "prioridad normal ("+err.Error()+")")
	} else {
		done = append(done, "prioridad por encima de lo normal")
	}
	done = append(done, disablePowerThrottling())
	if procDisableProcessWindowsGhosting.Find() == nil {
		procDisableProcessWindowsGhosting.Call()
		done = append(done, "sin ventana fantasma")
	}
	return strings.Join(done, ", ")
}

func disablePowerThrottling() string {
	if procSetProcessInformation.Find() != nil {
		return "power throttling sin cambiar (Windows sin SetProcessInformation)"
	}
	// IGNORE_TIMER_RESOLUTION existe desde Windows 11; un Windows 10 rechaza la máscara entera,
	// así que se reintenta solo con la velocidad de ejecución.
	var lastErr error
	for _, mask := range []uint32{powerThrottlingExecutionSpeed | powerThrottlingIgnoreTimerRes, powerThrottlingExecutionSpeed} {
		state := powerThrottlingState{Version: powerThrottlingCurrentVersion, ControlMask: mask}
		ok, _, err := procSetProcessInformation.Call(uintptr(windows.CurrentProcess()), processPowerThrottling,
			uintptr(unsafe.Pointer(&state)), unsafe.Sizeof(state))
		if ok != 0 {
			return "sin power throttling"
		}
		lastErr = err
	}
	return "power throttling sin cambiar (" + lastErr.Error() + ")"
}

var awake struct {
	sync.Mutex
	request windows.Handle // 0 = sin crear
	on      bool
}

// KeepSystemAwake impide (on) o vuelve a permitir que la PC se suspenda por inactividad. Se llama
// seguido (cada chequeo del monitor de audio): solo hace algo cuando cambia. Usa un pedido de
// energía con handle y no SetThreadExecutionState, que es por hilo y las goroutines cambian de
// hilo. No evita que se apague la pantalla. Devuelve un error solo si no se pudo cambiar.
func KeepSystemAwake(on bool) error {
	awake.Lock()
	defer awake.Unlock()
	if awake.on == on {
		return nil
	}
	if procPowerCreateRequest.Find() != nil {
		return procPowerCreateRequest.Find()
	}
	if awake.request == 0 {
		reason, err := windows.UTF16PtrFromString("Sala de voz de VoxMesh activa")
		if err != nil {
			return err
		}
		ctx := reasonContext{Version: powerRequestContextVersion, Flags: powerRequestContextSimpleString, SimpleReasonString: reason}
		handle, _, err := procPowerCreateRequest.Call(uintptr(unsafe.Pointer(&ctx)))
		if windows.Handle(handle) == windows.InvalidHandle || handle == 0 {
			return err
		}
		awake.request = windows.Handle(handle)
	}
	proc := procPowerClearRequest
	if on {
		proc = procPowerSetRequest
	}
	if ok, _, err := proc.Call(uintptr(awake.request), powerRequestSystemRequired); ok == 0 {
		return err
	}
	awake.on = on
	return nil
}
