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
	Inputs  []Device
	Outputs []Device
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
	return DeviceLists{Inputs: deviceList(inputs), Outputs: deviceList(outputs)}, nil
}

func deviceList(devices []malgo.DeviceInfo) []Device {
	list := make([]Device, 0, len(devices))
	for index := range devices {
		name := strings.TrimSpace(devices[index].Name())
		if name != "" {
			list = append(list, Device{Name: name, ID: devices[index].ID.String()})
		}
	}
	return list
}

// ResolveDevices le pide a Windows la lista de ahora y dice qué se abriría para cada elección
// (ver resolveDevice), sin abrir nada. Para "Refresh Audio" fuera de una sala.
func ResolveDevices(input, output Device) (Resolved, Resolved, error) {
	context, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return Resolved{Device: DefaultChoice}, Resolved{Device: DefaultChoice}, fmt.Errorf("inicializar audio: %w", err)
	}
	defer context.Free()
	defer context.Uninit()
	_, in, inErr := resolveDevice(context, malgo.Capture, input)
	_, out, outErr := resolveDevice(context, malgo.Playback, output)
	if inErr != nil {
		return in, out, inErr
	}
	return in, out, outErr
}

func RecordInput(choice Device, duration time.Duration) ([]byte, error) {
	context, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("inicializar audio: %w", err)
	}
	defer context.Free()
	defer context.Uninit()

	deviceID, _, _ := resolveDevice(context, malgo.Capture, choice)
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

func PlayTestTone(choice Device, duration time.Duration) error {
	context, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return fmt.Errorf("inicializar audio: %w", err)
	}
	defer context.Free()
	defer context.Uninit()

	deviceID, _, _ := resolveDevice(context, malgo.Playback, choice)
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

func PlayPCM(choice Device, data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("no hay una grabación en memoria")
	}
	context, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return fmt.Errorf("inicializar audio: %w", err)
	}
	defer context.Free()
	defer context.Uninit()

	deviceID, _, _ := resolveDevice(context, malgo.Playback, choice)
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

// resolveDevice le pide a Windows la lista de ahora y elige con PickDevice. Devuelve nil (abrir el
// predeterminado) cuando la elección es el predeterminado o ya no está: un dispositivo que se
// desconectó nunca deja sin audio. Si la lista no se puede pedir también abre el predeterminado,
// y el error es solo para el log.
func resolveDevice(context *malgo.AllocatedContext, kind malgo.DeviceType, want Device) (*malgo.DeviceID, Resolved, error) {
	if IsDefaultDevice(want.Name) && want.ID == "" {
		return nil, Resolved{Device: DefaultChoice}, nil
	}
	devices, err := context.Devices(kind)
	if err != nil {
		return nil, Resolved{Device: DefaultChoice, Missing: true}, fmt.Errorf("enumerar dispositivos: %w", err)
	}
	list := deviceList(devices)
	index, missing := PickDevice(list, want)
	if index < 0 {
		return nil, Resolved{Device: DefaultChoice, Missing: missing}, nil
	}
	for i := range devices {
		if devices[i].ID.String() == list[index].ID {
			return &devices[i].ID, Resolved{Device: list[index]}, nil
		}
	}
	return nil, Resolved{Device: DefaultChoice, Missing: true}, nil
}
