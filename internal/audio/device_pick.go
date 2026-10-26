package audio

import (
	"strings"

	"voxmesh/internal/config"
)

// Device es un dispositivo de audio de Windows: el nombre para mostrar y el ID del endpoint
// (malgo.DeviceID en hex), que no cambia aunque haya dos con el mismo nombre. Guardado en la
// config es la elección del usuario; Name = config.DefaultDevice (o vacío) es el predeterminado.
type Device struct {
	Name string
	ID   string
}

// DefaultChoice es "lo que diga Windows": sin ID, miniaudio abre el predeterminado y lo sigue si
// cambia.
var DefaultChoice = Device{Name: config.DefaultDevice}

// Resolved es el dispositivo que se abrió para una elección guardada.
type Resolved struct {
	// Device es el que se abrió (DefaultChoice si fue el predeterminado).
	Device Device
	// Missing: había uno elegido y Windows ya no lo tiene, así que se abrió el predeterminado.
	Missing bool
}

func IsDefaultDevice(name string) bool {
	return strings.TrimSpace(name) == "" || name == config.DefaultDevice
}

// PickDevice busca want en la lista que da Windows ahora: por ID, o por nombre si la elección no
// tiene ID (config de una versión anterior). Devuelve -1 si hay que abrir el predeterminado y
// missing si fue porque el elegido ya no está. No recuerda nada más: si no está, el predeterminado.
func PickDevice(list []Device, want Device) (index int, missing bool) {
	if IsDefaultDevice(want.Name) && want.ID == "" {
		return -1, false
	}
	for i, device := range list {
		if want.ID != "" && device.ID == want.ID {
			return i, false
		}
		if want.ID == "" && device.Name == want.Name {
			return i, false
		}
	}
	return -1, true
}
