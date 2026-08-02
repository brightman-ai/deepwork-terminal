package terminal

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

// End-to-end proof that the Agent Overview preview survives a REAL full-screen TUI.
//
// The unit tests in screen_test.go feed hand-written escape sequences. This one drives an actual
// repainting program (`top`) through an actual PTY, so the bytes are whatever the program really
// emits — the case that broke the first two implementations. `top` stands in for Claude/Codex:
// same mechanic (clear, position cursor, repaint the whole viewport every interval).
func TestRenderScreen_RealPTY_FullScreenTUI(t *testing.T) {
	if _, err := exec.LookPath("top"); err != nil {
		t.Skip("top not available")
	}
	if _, err := exec.LookPath("/bin/bash"); err != nil {
		t.Skip("bash not available")
	}

	mgr := NewSessionManager(1<<20, "/bin/bash")
	defer mgr.DestroyAll()

	sess, err := mgr.Create("tui")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := sess.PTY.Write([]byte("top -d 1\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Let it paint a couple of frames.
	time.Sleep(3 * time.Second)

	raw := string(sess.Buffer.ReadTail(sessionScreenScanBytes))
	if raw == "" {
		t.Skip("no PTY output captured in this environment")
	}
	lines := renderScreen(raw, testRows, testCols)
	joined := strings.Join(lines, "\n")

	// 1. No escape residue reaches the card.
	if strings.ContainsRune(joined, 0x1b) {
		t.Fatalf("escape sequences leaked into the rendered screen:\n%q", joined)
	}
	// 2. It is a SCREEN, not one concatenated line. The original defect produced exactly one
	//    enormous line of interleaved frames.
	if len(lines) < 3 {
		t.Fatalf("expected a multi-line screen, got %d line(s):\n%q", len(lines), joined)
	}
	// 3. It shows top's actual content, i.e. the final frame won rather than every frame piling up.
	if !strings.Contains(joined, "PID") && !strings.Contains(joined, "Tasks") && !strings.Contains(joined, "Mem") {
		t.Fatalf("rendered screen has none of top's landmarks — repaint was not reconstructed:\n%q", joined)
	}
	// 4. A repainting TUI must not leave dozens of duplicate header rows.
	if headers := strings.Count(joined, "Tasks:"); headers > 1 {
		t.Fatalf("frames stacked instead of overwriting (%d 'Tasks:' headers):\n%q", headers, joined)
	}

	t.Logf("rendered %d lines; first: %q", len(lines), lines[0])
}
