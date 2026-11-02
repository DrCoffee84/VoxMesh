//go:build !windows

package transport

import (
	"syscall"
)

func configureSocket(fd uintptr) error {
	return syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
}
