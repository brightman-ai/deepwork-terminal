package main

import "golang.org/x/sys/unix"

// Darwin (and the BSDs) spell them TIOCGETA/TIOCSETA. Same struct, different request
// numbers — which is exactly the difference a filename ending in _unix failed to express.
const (
	ioctlReadTermios  = unix.TIOCGETA
	ioctlWriteTermios = unix.TIOCSETA
)
