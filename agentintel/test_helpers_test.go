package agentintel

import (
	"os"
	"path/filepath"
	"testing"
)

// shortTestSocket avoids AF_UNIX path limits on macOS. t.TempDir includes the full test
// name and can make a valid local tmux fixture fail with "File name too long".
func shortTestSocket(t *testing.T, name string) string {
	t.Helper()
	dir, err := os.MkdirTemp(os.TempDir(), "dw-")
	if err != nil {
		t.Fatalf("make short socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, name)
}
