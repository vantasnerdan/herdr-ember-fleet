//go:build linux

package admin

import (
	"net"
	"os"

	"golang.org/x/sys/unix"
)

func sameUser(c *net.UnixConn) bool {
	raw, err := c.SyscallConn()
	if err != nil {
		return false
	}
	ok := false
	raw.Control(func(fd uintptr) {
		cred, err := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		ok = err == nil && int(cred.Uid) == os.Getuid()
	})
	return ok
}
