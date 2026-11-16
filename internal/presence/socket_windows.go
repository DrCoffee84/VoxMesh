//go:build windows

package presence

import (
	"errors"
	"syscall"
)

// setSocketOptions pide broadcast (Go ya lo prende en UDP IPv4; acá va explícito) y, en el puerto
// fijo, SO_REUSEADDR: al revés que el socket de la sala (SO_EXCLUSIVEADDRUSE, transport), acá
// varias instancias tienen que poder escuchar el mismo puerto.
func setSocketOptions(fd uintptr, shared bool) error {
	handle := syscall.Handle(fd)
	if shared {
		if err := syscall.SetsockoptInt(handle, syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1); err != nil {
			return err
		}
	}
	return syscall.SetsockoptInt(handle, syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1)
}

// wsaeMsgSize es WSAEMSGSIZE: el paquete no entró en el buffer de lectura.
const wsaeMsgSize = syscall.Errno(10040)

// messageTooLong dice si una lectura falló porque el paquete era más grande que el buffer: en
// Windows no se corta, la lectura da este error (y no dice de dónde vino).
func messageTooLong(err error) bool {
	return errors.Is(err, wsaeMsgSize)
}
