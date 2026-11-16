//go:build !windows

package presence

import "syscall"

// setSocketOptions: lo mismo que en Windows (socket_windows.go).
func setSocketOptions(fd uintptr, shared bool) error {
	if shared {
		if err := syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1); err != nil {
			return err
		}
	}
	return syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1)
}

// messageTooLong: fuera de Windows el paquete se corta al tamaño del buffer y Decode lo descarta
// por grande; la lectura no falla.
func messageTooLong(error) bool { return false }
