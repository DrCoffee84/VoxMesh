//go:build !windows

package winproc

// Configure no hace nada fuera de Windows.
func Configure() string { return "sin ajustes de proceso (no es Windows)" }

// KeepSystemAwake no hace nada fuera de Windows.
func KeepSystemAwake(bool) error { return nil }

// Instance no hace nada fuera de Windows: cada VoxMesh es la primera instancia.
type Instance struct{}

// SingleInstance siempre es la primera fuera de Windows.
func SingleInstance(string) (*Instance, bool) { return &Instance{}, true }

// RequestShow no hace nada fuera de Windows.
func (*Instance) RequestShow() {}

// OnShowRequest no hace nada fuera de Windows.
func (*Instance) OnShowRequest(func()) {}

// Autostart siempre es false fuera de Windows.
func Autostart() bool { return false }

// SetAutostart no hace nada fuera de Windows.
func SetAutostart(bool) error { return nil }

// RefreshAutostart no hace nada fuera de Windows.
func RefreshAutostart() (bool, error) { return false, nil }
