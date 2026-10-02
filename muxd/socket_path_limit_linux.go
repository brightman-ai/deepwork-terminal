//go:build linux

package muxd

// sockaddr_un.sun_path reserves a trailing NUL on Linux.
const maxUnixSocketPathBytes = 107
