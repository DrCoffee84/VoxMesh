//go:build !windows

package transport

import (
	"syscall"
)

func setReusePort(fd uintptr) error {
	return syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
}
