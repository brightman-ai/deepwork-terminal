//go:build darwin

package muxd

// sockaddr_un.sun_path reserves a trailing NUL on Darwin.
const maxUnixSocketPathBytes = 103
