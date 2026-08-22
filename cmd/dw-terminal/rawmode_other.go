//go:build !linux && !darwin

package main

import (
	"errors"
	"os"
)

// rawMode has no implementation outside Linux and macOS, which are the platforms this
// product ships. Failing here is a clear, local error rather than a build failure for the
// whole binary: `dw-terminal` still serves and still runs the daemon elsewhere; only
// `attach` needs a terminal it can put into raw mode.
func rawMode(*os.File) (func(), error) {
	return nil, errors.New("attach needs raw terminal mode, which is not implemented on this platform")
}

func terminalSize(*os.File) (cols, rows uint16, ok bool) { return 0, 0, false }
