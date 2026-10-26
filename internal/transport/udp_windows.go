//go:build windows

package transport

import (
	"syscall"
)

// soExclusiveAddrUse es SO_EXCLUSIVEADDRUSE de Winsock (~SO_REUSEADDR).
const soExclusiveAddrUse = ^syscall.SO_REUSEADDR

// configureSocket pide SO_EXCLUSIVEADDRUSE: una segunda instancia en el mismo puerto falla al
// abrir. Con SO_REUSEADDR se ligaba igual y Windows repartía los paquetes entre las dos. UDP no
// tiene TIME_WAIT, así que reabrir el puerto al reconectar sigue andando. (Los errores por ICMP
// "port unreachable" ya los apaga el paquete net de Go con SIO_UDP_CONNRESET.)
func configureSocket(fd uintptr) error {
	return syscall.SetsockoptInt(syscall.Handle(fd), syscall.SOL_SOCKET, soExclusiveAddrUse, 1)
}
