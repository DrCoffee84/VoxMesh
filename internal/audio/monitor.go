//go:build cgo

package audio

import (
	"strings"
	"sync"

	"github.com/gen2brain/malgo"

	"voxmesh/internal/config"
)

// Monitor abre el micrófono fuera de una sala, para el medidor de Ajustes.
type Monitor struct {
	context *malgo.AllocatedContext
	device  *malgo.Device
	once    sync.Once
	frames  chan [streamFrameBytes]byte
	buffer  []byte // re-armado en frames de 20 ms; solo lo toca el callback de captura
}

// monitorQueueFrames son los frames que pueden esperar a onFrame; si se atrasa más, se tiran
// (es solo el medidor).
const monitorQueueFrames = 8

// StartMonitor abre el micrófono sin sala y le pasa a onFrame cada frame de 20 ms (S16LE, el slice
// vale solo durante la llamada) desde una goroutine propia: fuera del hilo de captura, así quien
// lo usa puede correr la cadena de envío para saber si ese audio se mandaría.
func StartMonitor(name string, onFrame func(pcm []byte)) (*Monitor, error) {
	context, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, err
	}
	deviceID, err := findDeviceID(context, malgo.Capture, name)
	if err != nil {
		context.Free()
		return nil, err
	}
	deviceConfig := malgo.DefaultDeviceConfig(malgo.Capture)
	deviceConfig.Capture.Format = malgo.FormatS16
	deviceConfig.Capture.Channels = Channels
	deviceConfig.SampleRate = SampleRate
	deviceConfig.PerformanceProfile = malgo.LowLatency
	if deviceID != nil {
		deviceConfig.Capture.DeviceID = deviceID.Pointer()
	}
	monitor := &Monitor{
		context: context,
		frames:  make(chan [streamFrameBytes]byte, monitorQueueFrames),
		buffer:  make([]byte, 0, streamFrameBytes*4),
	}
	device, err := malgo.InitDevice(context.Context, deviceConfig, malgo.DeviceCallbacks{
		Data: func(_, input []byte, _ uint32) {
			monitor.buffer = append(monitor.buffer, input...)
			for len(monitor.buffer) >= streamFrameBytes {
				var frame [streamFrameBytes]byte // por valor: el canal lo copia, sin asignar memoria
				copy(frame[:], monitor.buffer)
				select {
				case monitor.frames <- frame:
				default:
				}
				monitor.buffer = monitor.buffer[:copy(monitor.buffer, monitor.buffer[streamFrameBytes:])]
			}
		},
	})
	if err != nil {
		context.Free()
		return nil, err
	}
	monitor.device = device
	if err := device.Start(); err != nil {
		device.Uninit()
		context.Free()
		return nil, err
	}
	go func() {
		for frame := range monitor.frames {
			onFrame(frame[:])
		}
	}()
	return monitor, nil
}

// Stop cierra el micrófono. Uninit espera a que termine el callback, así después ya nadie escribe
// en frames.
func (m *Monitor) Stop() {
	if m == nil {
		return
	}
	m.once.Do(func() {
		m.device.Uninit()
		m.context.Free()
		close(m.frames)
	})
}

func IsDefaultDevice(name string) bool {
	return strings.TrimSpace(name) == "" || name == config.DefaultDevice
}
