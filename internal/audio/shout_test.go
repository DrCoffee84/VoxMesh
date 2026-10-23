package audio

import "testing"

// shoutTestSettings: solo RNNoise, sin VAD/compuerta/limitador del usuario, para medir la mezcla.
func shoutTestSettings() FilterSettings {
	settings := DefaultFilterSettings()
	settings.VADEnabled = false
	settings.NoiseGateEnabled = false
	settings.LimiterEnabled = false
	settings.ShoutEnabled = false
	return settings
}

func TestShoutMix_DryIsAlignedWithRNNoiseDelay(t *testing.T) {
	denoiser, err := NewRNNoise()
	if err != nil {
		t.Fatal(err)
	}
	if denoiser.Delay() != FrameSamples {
		t.Fatalf("RNNoise delay %d, the dry delay line assumes any value but this test assumes one frame (%d)", denoiser.Delay(), FrameSamples)
	}
	effects := &Effects{}
	settings := shoutTestSettings()
	settings.RNNoiseDryMix = 1 // intensidad 0 %: sale solo la señal original, retrasada como RNNoise

	var previous []int16
	for frame := 0; frame < 6; frame++ {
		input := sineFrame(frame, 8000)
		frames, _, err := PrepareOutgoingFrames(append([]int16(nil), input...), denoiser, NoiseGate{}, true, 0.5, settings, effects)
		if err != nil {
			t.Fatal(err)
		}
		if len(frames) != 1 {
			t.Fatalf("frame %d: expected 1 frame, got %d", frame, len(frames))
		}
		// El primer frame rampea la mezcla desde 0; desde el segundo la salida es la entrada
		// del frame anterior, muestra por muestra.
		if frame >= 2 {
			for i, sample := range frames[0] {
				if sample != previous[i] {
					t.Fatalf("frame %d sample %d: got %d, want delayed input %d", frame, i, sample, previous[i])
				}
			}
		}
		previous = input
	}
}

func TestShout_ForcesVoiceAndSendsOriginalAligned(t *testing.T) {
	denoiser, err := NewRNNoise()
	if err != nil {
		t.Fatal(err)
	}
	effects := &Effects{}
	settings := shoutTestSettings()
	settings.VADEnabled = true
	settings.VADHoldMS = 0
	settings.ShoutEnabled = true
	settings.ShoutThresholdDB = -12
	settings.ShoutDryMix = 1
	settings.ShoutHoldMS = 0
	const vadThreshold = 0.99 // un seno no es voz: sin modo grito no saldría nada

	for frame := 0; frame < 3; frame++ {
		frames, _, err := PrepareOutgoingFrames(sineFrame(frame, 100), denoiser, NoiseGate{}, true, vadThreshold, settings, effects)
		if err != nil {
			t.Fatal(err)
		}
		if len(frames) != 0 {
			t.Fatalf("quiet frame %d was sent", frame)
		}
	}

	// Grito (RMS ≈ -7 dBFS): sale aunque el VAD no lo vea como voz.
	loud := sineFrame(3, 20000)
	frames, started, err := PrepareOutgoingFrames(append([]int16(nil), loud...), denoiser, NoiseGate{}, true, vadThreshold, settings, effects)
	if err != nil {
		t.Fatal(err)
	}
	if !started || len(frames) == 0 {
		t.Fatal("shout frame must open the gate")
	}

	// El frame siguiente trae el audio del grito (retardo de RNNoise): 100 % original, exacto.
	frames, _, err = PrepareOutgoingFrames(sineFrame(4, 20000), denoiser, NoiseGate{}, true, vadThreshold, settings, effects)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 1 {
		t.Fatalf("expected the shout to keep sending, got %d frames", len(frames))
	}
	for i, sample := range frames[0] {
		if sample != loud[i] {
			t.Fatalf("sample %d: got %d, want original shout %d", i, sample, loud[i])
		}
	}
}

func TestShoutMix_DefaultLeavesFilteredOutputUntouched(t *testing.T) {
	effects := &Effects{}
	settings := shoutTestSettings() // intensidad 100 %, modo grito apagado
	effects.shoutBegin(sineFrame(0, 20000), FrameSamples, settings)
	filtered := sineFrame(1, 5000)
	want := append([]int16(nil), filtered...)
	effects.shoutMix(filtered, false, settings)
	for i := range filtered {
		if filtered[i] != want[i] {
			t.Fatalf("sample %d changed: %d → %d", i, want[i], filtered[i])
		}
	}
}

func TestShoutBegin_HoldCountsFromTheDelayedFrame(t *testing.T) {
	effects := &Effects{}
	settings := shoutTestSettings()
	settings.ShoutEnabled = true
	settings.ShoutThresholdDB = -12
	settings.ShoutHoldMS = 40 // 2 frames

	quiet, loud := sineFrame(0, 100), sineFrame(0, 20000)
	if effects.shoutBegin(quiet, FrameSamples, settings) {
		t.Fatal("quiet frame activated shout mode")
	}
	// Grito en un frame: activo en ese (rampa), el siguiente (trae el grito) y 2 de hold.
	got := []bool{effects.shoutBegin(loud, FrameSamples, settings)}
	for i := 0; i < 4; i++ {
		got = append(got, effects.shoutBegin(quiet, FrameSamples, settings))
	}
	want := []bool{true, true, true, true, false}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("shout mode per frame = %v, want %v", got, want)
		}
	}
}

func TestShoutBegin_ClipTriggersBelowThreshold(t *testing.T) {
	settings := shoutTestSettings()
	settings.ShoutEnabled = true
	settings.ShoutThresholdDB = -3 // un frame que satura de a ratos no llega a este RMS
	settings.ShoutHoldMS = 0
	clippy := sineFrame(0, 3000) // RMS ≈ -23 dBFS
	clippy[100], clippy[101] = 32767, -32768

	off := settings
	off.ShoutOnClip = false
	if (&Effects{}).shoutBegin(clippy, FrameSamples, off) {
		t.Fatal("sin ShoutOnClip un pico suelto no activa el modo grito")
	}
	settings.ShoutOnClip = true
	if !(&Effects{}).shoutBegin(clippy, FrameSamples, settings) {
		t.Fatal("con ShoutOnClip la saturación tiene que activar el modo grito")
	}
}

func TestFrameLevel(t *testing.T) {
	if db, clipped := FrameLevel(EncodePCM(make([]int16, FrameSamples))); db != LevelFloorDB || clipped {
		t.Fatalf("silencio: %v dB, saturado %v", db, clipped)
	}
	frame := sineFrame(0, 16384) // pico -6 dBFS, RMS ≈ -9 dBFS
	if db, clipped := FrameLevel(EncodePCM(frame)); db < -10 || db > -8 || clipped {
		t.Fatalf("seno a -6 dBFS: %v dB, saturado %v", db, clipped)
	}
	frame[10] = -32768
	if _, clipped := FrameLevel(EncodePCM(frame)); !clipped {
		t.Fatal("un pico al tope tiene que marcar saturación")
	}
}
