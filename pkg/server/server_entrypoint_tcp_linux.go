// +build linux

package server

import (
	"syscall"

	"golang.org/x/sys/unix"
)

func init() {
	osNetControl = func(network, address string, c syscall.RawConn) error {
		var err error
		controlFunc := func(fd uintptr) {
			err = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1)
			if err != nil {
				return
			}

			err = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEPORT, 1)
			if err != nil {
				return
			}
		}
		if errC := c.Control(controlFunc); errC != nil {
			return errC
		}
		return err
	}
}
