//go:build darwin && !cgo

package terminal

func nativeProcessCWD(pid int) string { return "" }
