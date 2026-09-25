//go:build cgo

package audio

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/gen2brain/malgo"
)

type DeviceLists struct {
	Inputs  []string
	Outputs []string
}

func ListDevices() (DeviceLists, error) {
	context, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return DeviceLists{}, fmt.Errorf("inicializar audio: %w", err)
	}
	defer context.Free()
	defer context.Uninit()

	inputs, err := context.Devices(malgo.Capture)
	if err != nil {
		return DeviceLists{}, fmt.Errorf("enumerar entradas: %w", err)
	}
	outputs, err := context.Devices(malgo.Playback)
	if err != nil {
		return DeviceLists{}, fmt.Errorf("enumerar salidas: %w", err)
	}
	return DeviceLists{Inputs: deviceNames(inputs), Outputs: deviceNames(outputs)}, nil
}

func deviceNames(devices []malgo.DeviceInfo) []string {
	names := make([]string, 0, len(devices))
	for _, device := range devices {
		name := strings.TrimSpace(device.Name())
		if name != "" {
			names = append(names, name)
		}
	}
	return names
}

func RecordInput(name string, duration time.Duration) ([]byte, error) {
	context, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("inicializar audio: %w", err)
	}
	defer context.Free()
	defer context.Uninit()

	deviceID, err := findDeviceID(context, malgo.Capture, name)
	if err != nil {
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

	var recorded []byte
	device, err := malgo.InitDevice(context.Context, deviceConfig, malgo.DeviceCallbacks{
		Data: func(_, input []byte, _ uint32) {
			recorded = append(recorded, input...)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("abrir entrada de audio: %w", err)
	}
	defer device.Uninit()
	if err := device.Start(); err != nil {
		return nil, fmt.Errorf("iniciar grabación: %w", err)
	}
	time.Sleep(duration)
	return recorded, nil
}

func PlayTestTone(name string, duration time.Duration) error {
	context, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return fmt.Errorf("inicializar audio: %w", err)
	}
	defer context.Free()
	defer context.Uninit()

	deviceID, err := findDeviceID(context, malgo.Playback, name)
	if err != nil {
		return err
	}
	deviceConfig := malgo.DefaultDeviceConfig(malgo.Playback)
	deviceConfig.Playback.Format = malgo.FormatS16
	deviceConfig.Playback.Channels = Channels
	deviceConfig.SampleRate = SampleRate
	deviceConfig.PerformanceProfile = malgo.LowLatency
	if deviceID != nil {
		deviceConfig.Playback.DeviceID = deviceID.Pointer()
	}

	var phase float64
	device, err := malgo.InitDevice(context.Context, deviceConfig, malgo.DeviceCallbacks{
		Data: func(output, _ []byte, frameCount uint32) {
			for frame := uint32(0); frame < frameCount; frame++ {
				sample := int16(math.Sin(phase) * 5000)
				phase += 2 * math.Pi * 440 / SampleRate
				for channel := uint32(0); channel < Channels; channel++ {
					index := (frame*Channels + channel) * 2
					output[index] = byte(sample)
					output[index+1] = byte(sample >> 8)
				}
			}
		},
	})
	if err != nil {
		return fmt.Errorf("abrir salida de audio: %w", err)
	}
	defer device.Uninit()
	if err := device.Start(); err != nil {
		return fmt.Errorf("iniciar reproducción: %w", err)
	}
	time.Sleep(duration)
	return nil
}

func PlayPCM(name string, data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("no hay una grabación en memoria")
	}
	context, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return fmt.Errorf("inicializar audio: %w", err)
	}
	defer context.Free()
	defer context.Uninit()

	deviceID, err := findDeviceID(context, malgo.Playback, name)
	if err != nil {
		return err
	}
	deviceConfig := malgo.DefaultDeviceConfig(malgo.Playback)
	deviceConfig.Playback.Format = malgo.FormatS16
	deviceConfig.Playback.Channels = Channels
	deviceConfig.SampleRate = SampleRate
	deviceConfig.PerformanceProfile = malgo.LowLatency
	if deviceID != nil {
		deviceConfig.Playback.DeviceID = deviceID.Pointer()
	}

	position := 0
	device, err := malgo.InitDevice(context.Context, deviceConfig, malgo.DeviceCallbacks{
		Data: func(output, _ []byte, _ uint32) {
			if position >= len(data) {
				for index := range output {
					output[index] = 0
				}
				return
			}
			written := copy(output, data[position:])
			position += written
			for index := written; index < len(output); index++ {
				output[index] = 0
			}
		},
	})
	if err != nil {
		return fmt.Errorf("abrir salida de audio: %w", err)
	}
	defer device.Uninit()
	if err := device.Start(); err != nil {
		return fmt.Errorf("iniciar reproducción: %w", err)
	}
	duration := time.Duration(len(data)) * time.Second / (SampleRate * Channels * 2)
	time.Sleep(duration + 100*time.Millisecond)
	return nil
}

func findDeviceID(context *malgo.AllocatedContext, kind malgo.DeviceType, name string) (*malgo.DeviceID, error) {
	if strings.TrimSpace(name) == "" || name == "Sistema predeterminado" {
		return nil, nil
	}
	devices, err := context.Devices(kind)
	if err != nil {
		return nil, fmt.Errorf("enumerar dispositivos: %w", err)
	}
	for index := range devices {
		if devices[index].Name() == name {
			return &devices[index].ID, nil
		}
	}
	return nil, fmt.Errorf("dispositivo no encontrado: %s", name)
}
