package muxd

import "syscall"

// syscallKillZero reports whether a pid is still alive, without touching it.
// Signal 0 performs the permission and existence checks and delivers nothing — the
// standard way to ask "is this process still there?".
func syscallKillZero(pid int) error {
	return syscall.Kill(pid, 0)
}
