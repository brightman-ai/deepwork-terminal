//go:build darwin

package terminal

import (
	"context"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestReadClipboardFilePaths_ScriptRuns executes the real JXA against the real pasteboard.
//
// It asserts only that the script RUNS, because that is the failure mode worth a test: the
// script is a string in a Go file, so nothing else — not the compiler, not vet, not CI,
// which builds Linux — would notice a typo in it until a user pasted a file and got a 500.
// It deliberately does NOT put file URLs on the clipboard first: a test that clobbers the
// developer's clipboard to prove a point is a bad trade, and the multi-file read was
// verified by hand (two files in, two absolute paths out).
//
// Whatever happens to be on the pasteboard is fine: text yields an empty result, files
// yield paths, and both are a pass. The one thing that must not happen is an error.
func TestReadClipboardFilePaths_ScriptRuns(t *testing.T) {
	if _, err := exec.LookPath("/usr/bin/osascript"); err != nil {
		t.Skip("no osascript on this machine")
	}
	ctx, cancel := context.WithTimeout(context.Background(), nativeClipboardTimeout)
	defer cancel()

	paths, err := readClipboardFilePaths(ctx)

	require.NoError(t, err, "the JXA must run and return parseable JSON against whatever is on the pasteboard")
	for _, p := range paths {
		require.True(t, len(p) > 0 && p[0] == '/', "a pasteboard file URL must yield an absolute path, got %q", p)
	}
}
