//go:build linux

package agentintel

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func processEnvironment(pid int) ([]string, error) {
	raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "environ"))
	if err != nil {
		return nil, err
	}
	return strings.Split(string(raw), "\x00"), nil
}
