//go:build darwin

package herdr

import (
	"net"

	"golang.org/x/sys/unix"
)

// PeerOf identifies the process on the other end of a Unix socket.
func PeerOf(c *net.UnixConn) (Peer, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return Peer{}, err
	}
	var pid int
	var cred *unix.Xucred
	var perr, cerr error
	if err := raw.Control(func(fd uintptr) {
		pid, perr = unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID)
		cred, cerr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	}); err != nil {
		return Peer{}, err
	}
	if perr != nil {
		return Peer{}, perr
	}
	if cerr != nil {
		return Peer{}, cerr
	}
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return Peer{}, err
	}
	tv := kp.Proc.P_starttime
	return Peer{PID: pid, UID: int(cred.Uid), Start: int64(tv.Sec)*1_000_000 + int64(tv.Usec)}, nil
}
