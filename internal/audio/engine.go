//go:build cgo

package audio

import (
	"fmt"
	"sync"

	"github.com/gen2brain/malgo"
)

const streamFrameBytes = FrameSamples * 2 * Channels

type Engine struct {
	context       *malgo.AllocatedContext
	capture       *malgo.Device
	playback      *malgo.Device
	captureFrames chan []byte
	playbackQueue chan []byte
	captureBuffer []byte
	captureMu     sync.Mutex
	playbackData  []byte
	playbackMu    sync.Mutex
	stopOnce      sync.Once
}

func NewEngine(inputName, outputName string, onFrame func([]byte)) (*Engine, error) {
	context, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("inicializar motor de audio: %w", err)
	}
	engine := &Engine{
		context:       context,
		captureFrames: make(chan []byte, 8),
		playbackQueue: make(chan []byte, 32),
	}
	inputID, err := findDeviceID(context, malgo.Capture, inputName)
	if err != nil {
		engine.Stop()
		return nil, err
	}
	outputID, err := findDeviceID(context, malgo.Playback, outputName)
	if err != nil {
		engine.Stop()
		return nil, err
	}

	captureConfig := malgo.DefaultDeviceConfig(malgo.Capture)
	captureConfig.Capture.Format = malgo.FormatS16
	captureConfig.Capture.Channels = Channels
	captureConfig.SampleRate = SampleRate
	captureConfig.PerformanceProfile = malgo.LowLatency
	if inputID != nil {
		captureConfig.Capture.DeviceID = inputID.Pointer()
	}
	capture, err := malgo.InitDevice(context.Context, captureConfig, malgo.DeviceCallbacks{
		Data: func(_, input []byte, _ uint32) {
			engine.captureMu.Lock()
			engine.captureBuffer = append(engine.captureBuffer, input...)
			for len(engine.captureBuffer) >= streamFrameBytes {
				frame := append([]byte(nil), engine.captureBuffer[:streamFrameBytes]...)
				engine.captureBuffer = engine.captureBuffer[streamFrameBytes:]
				select {
				case engine.captureFrames <- frame:
				default:
				}
			}
			engine.captureMu.Unlock()
		},
	})
	if err != nil {
		engine.Stop()
		return nil, fmt.Errorf("abrir micrófono: %w", err)
	}
	engine.capture = capture

	playbackConfig := malgo.DefaultDeviceConfig(malgo.Playback)
	playbackConfig.Playback.Format = malgo.FormatS16
	playbackConfig.Playback.Channels = Channels
	playbackConfig.SampleRate = SampleRate
	playbackConfig.PerformanceProfile = malgo.LowLatency
	if outputID != nil {
		playbackConfig.Playback.DeviceID = outputID.Pointer()
	}
	playback, err := malgo.InitDevice(context.Context, playbackConfig, malgo.DeviceCallbacks{
		Data: func(output, _ []byte, _ uint32) {
			engine.playbackMu.Lock()
			defer engine.playbackMu.Unlock()
			written := 0
			for written < len(output) {
				if len(engine.playbackData) == 0 {
					select {
					case engine.playbackData = <-engine.playbackQueue:
					default:
						for index := written; index < len(output); index++ {
							output[index] = 0
						}
						return
					}
				}
				copied := copy(output[written:], engine.playbackData)
				written += copied
				engine.playbackData = engine.playbackData[copied:]
			}
		},
	})
	if err != nil {
		engine.Stop()
		return nil, fmt.Errorf("abrir salida: %w", err)
	}
	engine.playback = playback
	if err := engine.capture.Start(); err != nil {
		engine.Stop()
		return nil, fmt.Errorf("iniciar micrófono: %w", err)
	}
	if err := engine.playback.Start(); err != nil {
		engine.Stop()
		return nil, fmt.Errorf("iniciar salida: %w", err)
	}
	go func() {
		for frame := range engine.captureFrames {
			onFrame(frame)
		}
	}()
	return engine, nil
}

func (e *Engine) Play(data []byte) {
	if e == nil || len(data) == 0 {
		return
	}
	select {
	case e.playbackQueue <- append([]byte(nil), data...):
	default:
	}
}

func (e *Engine) Stop() {
	if e == nil {
		return
	}
	e.stopOnce.Do(func() {
		if e.capture != nil {
			e.capture.Uninit()
		}
		if e.playback != nil {
			e.playback.Uninit()
		}
		close(e.captureFrames)
		if e.context != nil {
			e.context.Free()
		}
	})
}
