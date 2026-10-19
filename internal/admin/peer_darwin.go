//go:build darwin

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
		cred, err := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		ok = err == nil && int(cred.Uid) == os.Getuid()
	})
	return ok
}
