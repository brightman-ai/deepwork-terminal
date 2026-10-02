//go:build darwin

package agentintel

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// macOS has no /proc. KERN_PROCARGS2 returns argv and, on systems that expose it, the
// process environment after argv. Some macOS configurations omit that tail; callers then
// fall back to the host's configured profile rather than guessing another one.
func processEnvironment(pid int) ([]string, error) {
	if pid <= 0 {
		return nil, errors.New("invalid process pid")
	}
	if pid == os.Getpid() {
		return os.Environ(), nil
	}
	raw, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return nil, err
	}
	if len(raw) < 4 {
		return nil, errors.New("short KERN_PROCARGS2 response")
	}
	argc := int(binary.NativeEndian.Uint32(raw[:4]))
	if argc < 0 || argc > len(raw) {
		return nil, fmt.Errorf("invalid KERN_PROCARGS2 argc %d", argc)
	}
	pos := 4
	for pos < len(raw) && raw[pos] == 0 {
		pos++ // kernel pads the executable path area with NUL bytes
	}
	for i := 0; i < argc; i++ {
		if pos >= len(raw) {
			return nil, errors.New("truncated KERN_PROCARGS2 argv")
		}
		end := bytes.IndexByte(raw[pos:], 0)
		if end < 0 {
			return nil, errors.New("unterminated KERN_PROCARGS2 argv")
		}
		pos += end + 1
	}
	var env []string
	for pos < len(raw) {
		end := bytes.IndexByte(raw[pos:], 0)
		if end < 0 {
			end = len(raw) - pos
		}
		if end == 0 {
			break
		}
		env = append(env, string(raw[pos:pos+end]))
		pos += end + 1
	}
	if len(env) == 0 {
		return nil, errors.New("macOS did not expose this process environment")
	}
	return env, nil
}
