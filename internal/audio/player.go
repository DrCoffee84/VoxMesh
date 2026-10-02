//go:build cgo

package audio

import (
	"fmt"
	"sync"

	"github.com/gen2brain/malgo"
)

type Player struct {
	context *malgo.AllocatedContext
	device  *malgo.Device
	queue   chan []byte
	data    []byte
	mu      sync.Mutex
	once    sync.Once
}

func NewPlayer(outputName string) (*Player, error) {
	context, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("inicializar salida de audio: %w", err)
	}
	player := &Player{context: context, queue: make(chan []byte, 32)}
	outputID, err := findDeviceID(context, malgo.Playback, outputName)
	if err != nil {
		player.Stop()
		return nil, err
	}
	deviceConfig := malgo.DefaultDeviceConfig(malgo.Playback)
	deviceConfig.Playback.Format = malgo.FormatS16
	deviceConfig.Playback.Channels = Channels
	deviceConfig.SampleRate = SampleRate
	deviceConfig.PerformanceProfile = malgo.LowLatency
	if outputID != nil {
		deviceConfig.Playback.DeviceID = outputID.Pointer()
	}
	device, err := malgo.InitDevice(context.Context, deviceConfig, malgo.DeviceCallbacks{
		Data: func(output, _ []byte, _ uint32) {
			player.mu.Lock()
			defer player.mu.Unlock()
			written := 0
			for written < len(output) {
				if len(player.data) == 0 {
					player.data = nil
					select {
					case player.data = <-player.queue:
					default:
						for index := written; index < len(output); index++ {
							output[index] = 0
						}
						return
					}
				}
				copied := copy(output[written:], player.data)
				written += copied
				player.data = player.data[copied:]
				if len(player.data) == 0 {
					player.data = nil
				}
			}
		},
	})
	if err != nil {
		player.Stop()
		return nil, fmt.Errorf("abrir salida de audio: %w", err)
	}
	player.device = device
	if err := player.device.Start(); err != nil {
		player.Stop()
		return nil, fmt.Errorf("iniciar salida de audio: %w", err)
	}
	return player, nil
}

func (p *Player) Play(data []byte) {
	if p == nil || len(data) == 0 {
		return
	}
	select {
	case p.queue <- data:
	default:
	}
}

func (p *Player) Stop() {
	if p == nil {
		return
	}
	p.once.Do(func() {
		if p.device != nil {
			p.device.Uninit()
		}
		if p.context != nil {
			p.context.Free()
		}
	})
}

// Clear drops any queued/in-progress audio without tearing down the device,
// so a new clip can start immediately (used to interrupt a soundboard clip).
func (p *Player) Clear() {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.data = nil
	p.mu.Unlock()
	for {
		select {
		case <-p.queue:
		default:
			return
		}
	}
}
