package main

import "golang.org/x/sys/unix"

// Linux spells the termios ioctls TCGETS/TCSETS.
const (
	ioctlReadTermios  = unix.TCGETS
	ioctlWriteTermios = unix.TCSETS
)
