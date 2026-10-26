package audio

import "time"

// DeviceSilenceLimit es cuánto puede pasar sin callbacks de un dispositivo abierto antes de darlo
// por caído. malgo llama cada ~10 ms; segundos sin llamar es un dispositivo que Windows invalidó
// (USB/Bluetooth desconectado, vuelta de una suspensión, cambio de driver) sin avisar con Stop.
const DeviceSilenceLimit = 3 * time.Second

// deviceHealth decide si el audio sigue vivo. started, lastCapture y lastPlayback son UnixNano
// (0 = nunca hubo callback); lost es el dispositivo que avisó que se detuvo ("" = ninguno). Los
// primeros DeviceSilenceLimit después de abrir no cuentan: WASAPI tarda en arrancar.
func deviceHealth(now time.Time, started, lastCapture, lastPlayback int64, lost string) (ok bool, reason string) {
	if lost != "" {
		return false, lost + " detenido por el sistema"
	}
	limit := now.Add(-DeviceSilenceLimit).UnixNano()
	if started > limit {
		return true, ""
	}
	if max(lastCapture, started) < limit {
		return false, "el micrófono dejó de entregar audio"
	}
	if max(lastPlayback, started) < limit {
		return false, "la salida dejó de pedir audio"
	}
	return true, ""
}
