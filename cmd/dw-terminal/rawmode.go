//go:build linux || darwin

package main

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// rawMode puts a terminal into character-at-a-time mode and returns a restore function.
//
// Written against x/sys/unix (already a dependency) rather than pulling in x/term: this is
// twenty lines of termios, and a public module's go.mod is worth more than the twenty
// lines are.
//
// The ioctl REQUEST numbers differ per platform, which is why they live in
// rawmode_linux.go / rawmode_darwin.go instead of here. The first version of this file was
// named rawmode_unix.go with the Linux constants inline — and `_unix` is not a GOOS suffix
// Go recognises, so it compiled everywhere and broke the macOS build outright. macOS is a
// first-class target (Homebrew, universal binary), so that was a release blocker sitting
// behind a filename that merely looked like a build constraint.
//
// The restore function MUST run on every exit path, including signals. A process that dies
// without restoring leaves the user's shell with no echo and no line editing — the terminal
// looks broken and `reset` is the only cure, which is a miserable thing to inflict on
// someone whose session just crashed.
func rawMode(f *os.File) (restore func(), err error) {
	fd := int(f.Fd())
	prev, err := unix.IoctlGetTermios(fd, ioctlReadTermios)
	if err != nil {
		return nil, fmt.Errorf("not a terminal (%s): %w", f.Name(), err)
	}
	raw := *prev
	// Straight from cfmakeraw: no echo, no canonical line editing, no signal generation
	// from keys, no CR/NL translation, no flow control, 8-bit clean.
	raw.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP |
		unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
	raw.Oflag &^= unix.OPOST
	raw.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	raw.Cflag &^= unix.CSIZE | unix.PARENB
	raw.Cflag |= unix.CS8
	// Block until at least one byte; no inter-byte timer. A read that returned empty in a
	// tight loop would spin a core for nothing.
	raw.Cc[unix.VMIN] = 1
	raw.Cc[unix.VTIME] = 0
	if err := unix.IoctlSetTermios(fd, ioctlWriteTermios, &raw); err != nil {
		return nil, fmt.Errorf("cannot enter raw mode: %w", err)
	}
	return func() { _ = unix.IoctlSetTermios(fd, ioctlWriteTermios, prev) }, nil
}

// terminalSize reports the window size of f, or ok=false when f is not a terminal.
func terminalSize(f *os.File) (cols, rows uint16, ok bool) {
	ws, err := unix.IoctlGetWinsize(int(f.Fd()), unix.TIOCGWINSZ)
	if err != nil || ws.Col == 0 || ws.Row == 0 {
		return 0, 0, false
	}
	return ws.Col, ws.Row, true
}
