//go:build cgo

package audio

import (
	"encoding/binary"
	"math"
	"strings"
	"sync"

	"github.com/gen2brain/malgo"
)

type Monitor struct {
	context *malgo.AllocatedContext
	device  *malgo.Device
	once    sync.Once
}

func StartMonitor(name string, callback func(float32)) (*Monitor, error) {
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
			var sum float64
			count := len(input) / 2
			for index := 0; index < count; index++ {
				sample := float64(int16(binary.LittleEndian.Uint16(input[index*2:]))) / math.MaxInt16
				sum += sample * sample
			}
			if count == 0 {
				return
			}
			level := float32(20 * math.Log10(math.Sqrt(sum/float64(count))))
			if math.IsInf(float64(level), -1) || level < -60 {
				level = -60
			}
			if level > 0 {
				level = 0
			}
			callback(level)
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
	return strings.TrimSpace(name) == "" || name == "Sistema predeterminado"
}
