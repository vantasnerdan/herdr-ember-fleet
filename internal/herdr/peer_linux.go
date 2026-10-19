//go:build linux

package herdr

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// PeerOf identifies the process on the other end of a Unix socket.
func PeerOf(c *net.UnixConn) (Peer, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return Peer{}, err
	}
	var cred *unix.Ucred
	var cerr error
	if err := raw.Control(func(fd uintptr) {
		cred, cerr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return Peer{}, err
	}
	if cerr != nil {
		return Peer{}, cerr
	}
	start, err := startTime(int(cred.Pid))
	if err != nil {
		return Peer{}, err
	}
	return Peer{PID: int(cred.Pid), UID: int(cred.Uid), Start: start}, nil
}

// startTime is the process start time in clock ticks since boot (/proc/PID/stat field 22).
func startTime(pid int) (int64, error) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, err
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return 0, fmt.Errorf("bad /proc stat")
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 20 {
		return 0, fmt.Errorf("short /proc stat")
	}
	return strconv.ParseInt(f[19], 10, 64)
}
