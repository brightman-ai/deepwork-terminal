package muxd

import (
	"net"

	"golang.org/x/sys/unix"
)

// peerPID is the macOS spelling of the same idea as the Linux one — see peer_linux.go
// for why this lives outside the protocol. Darwin has no SO_PEERCRED; LOCAL_PEERPID is
// the equivalent, and it returns only the pid (uid/gid would need LOCAL_PEERCRED, which
// this does not need).
func peerPID(conn net.Conn) (int, bool) {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return 0, false
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return 0, false
	}
	var pid int
	if err := raw.Control(func(fd uintptr) {
		p, cerr := unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID)
		if cerr == nil {
			pid = p
		}
	}); err != nil {
		return 0, false
	}
	return pid, pid > 0
}
