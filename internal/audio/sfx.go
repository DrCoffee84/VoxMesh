package audio

import "math"

// SFX bytes are 48kHz, 16-bit mono PCM.
var (
	SFXConnect    = generateTwoTone(523.25, 659.25, 0.09, 0.14, 0.65) // C5 -> E5
	SFXDisconnect = generateTwoTone(659.25, 440.00, 0.09, 0.14, 0.65) // E5 -> A4
	SFXMessage    = generateBlip(880.0, 0.06, 0.55)                   // A5 blip
	// Atajos de la botonera prendidos / apagados (AGENDA C20): más agudos y cortos que conectar y
	// desconectar, para no confundirlos con alguien que entra o sale.
	SFXHotkeysOn  = generateTwoTone(783.99, 1046.50, 0.06, 0.09, 0.6) // G5 -> C6
	SFXHotkeysOff = generateTwoTone(1046.50, 783.99, 0.06, 0.09, 0.6) // C6 -> G5
	// Timbre de una llamada de "En tu red" (F9): dos repiques, se repite cada 2 s mientras el
	// cartel está abierto.
	SFXRing = append(generateTwoTone(880.00, 1108.73, 0.12, 0.12, 0.55), generateTwoTone(880.00, 1108.73, 0.12, 0.18, 0.55)...) // A5 -> C#6, dos veces
)

func generateTwoTone(freq1, freq2 float64, dur1, dur2 float64, gain float64) []byte {
	samples1 := int(dur1 * float64(SampleRate))
	samples2 := int(dur2 * float64(SampleRate))
	total := samples1 + samples2
	out := make([]byte, total*2)

	for i := 0; i < samples1; i++ {
		t := float64(i) / float64(SampleRate)
		env := math.Exp(-4.0 * (float64(i) / float64(samples1)))
		sample := int16(math.Sin(2*math.Pi*freq1*t) * env * gain * 32767.0)
		out[i*2] = byte(sample)
		out[i*2+1] = byte(sample >> 8)
	}

	for i := 0; i < samples2; i++ {
		t := float64(i) / float64(SampleRate)
		env := math.Exp(-4.0 * (float64(i) / float64(samples2)))
		sample := int16(math.Sin(2*math.Pi*freq2*t) * env * gain * 32767.0)
		idx := (samples1 + i) * 2
		out[idx] = byte(sample)
		out[idx+1] = byte(sample >> 8)
	}

	return out
}

func generateBlip(freq float64, duration float64, gain float64) []byte {
	samples := int(duration * float64(SampleRate))
	out := make([]byte, samples*2)
	for i := 0; i < samples; i++ {
		t := float64(i) / float64(SampleRate)
		env := math.Exp(-8.0 * (float64(i) / float64(samples)))
		sample := int16(math.Sin(2*math.Pi*freq*t) * env * gain * 32767.0)
		out[i*2] = byte(sample)
		out[i*2+1] = byte(sample >> 8)
	}
	return out
}
