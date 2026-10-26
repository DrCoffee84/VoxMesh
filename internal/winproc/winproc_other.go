//go:build !windows

package winproc

// Configure no hace nada fuera de Windows.
func Configure() string { return "sin ajustes de proceso (no es Windows)" }

// KeepSystemAwake no hace nada fuera de Windows.
func KeepSystemAwake(bool) error { return nil }
