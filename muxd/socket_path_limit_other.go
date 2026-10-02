//go:build !darwin && !linux

package muxd

// The supported Unix targets with known sun_path limits define this constant directly.
const maxUnixSocketPathBytes = 0
