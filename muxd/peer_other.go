//go:build !linux && !darwin

package muxd

import "net"

// peerPID has no portable answer outside Linux and macOS. Callers already treat the
// pid as best-effort — an unknown pid costs an upgrade message some of its detail, not
// its meaning.
func peerPID(net.Conn) (int, bool) { return 0, false }
