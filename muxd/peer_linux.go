package muxd

import (
	"net"

	"golang.org/x/sys/unix"
)

// peerPID reports the pid on the other end of a unix socket, from the kernel's own
// record of who connected.
//
// This is deliberately OUTSIDE the protocol. Everything the daemon says about itself can
// become unreadable the day the wire format changes incompatibly — which is precisely
// the moment a user most needs to be told which process is holding their shells. Peer
// credentials cannot go stale that way: the kernel answers, not the daemon, and the
// answer is unforgeable (a process cannot claim someone else's pid here).
func peerPID(conn net.Conn) (int, bool) {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return 0, false
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return 0, false
	}
	var (
		pid    int
		called bool
	)
	if err := raw.Control(func(fd uintptr) {
		cred, cerr := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		if cerr == nil && cred != nil {
			pid = int(cred.Pid)
			called = true
		}
	}); err != nil {
		return 0, false
	}
	return pid, called && pid > 0
}
