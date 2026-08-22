package terminal

import "syscall"

// syscallKillZeroForTest reports whether a pid is still alive without touching it.
// Signal 0 performs the existence/permission checks and delivers nothing.
func syscallKillZeroForTest(pid int) error { return syscall.Kill(pid, 0) }
