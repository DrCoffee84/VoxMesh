//go:build cgo

package audio

import (
	"strings"
	"sync"

	"github.com/gen2brain/malgo"

	"voxmesh/internal/config"
)

type Monitor struct {
	context *malgo.AllocatedContext
	device  *malgo.Device
	once    sync.Once
}

// StartMonitor abre el micrófono solo para medirlo: callback recibe el nivel y si saturó
// (FrameLevel) por cada buffer de captura.
func StartMonitor(name string, callback func(db float32, clipped bool)) (*Monitor, error) {
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
	monitor := &Monitor{context: context}
	device, err := malgo.InitDevice(context.Context, deviceConfig, malgo.DeviceCallbacks{
		Data: func(_, input []byte, _ uint32) {
			if len(input) < 2 {
				return
			}
			callback(FrameLevel(input))
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
	return monitor, nil
}

func (m *Monitor) Stop() {
	if m == nil {
		return
	}
	m.once.Do(func() {
		m.device.Uninit()
		m.context.Free()
	})
}

func IsDefaultDevice(name string) bool {
	return strings.TrimSpace(name) == "" || name == config.DefaultDevice
}
