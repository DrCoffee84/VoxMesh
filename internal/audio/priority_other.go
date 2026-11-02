//go:build !windows

package audio

// LockAudioThread no cambia la prioridad fuera de Windows (la app es para Windows: esto es para
// que compile y corran los tests en otros sistemas).
func LockAudioThread() string { return "prioridad normal" }
