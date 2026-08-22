package muxd

import (
	"os"
	"strings"
	"testing"
)

// TestDaemonBinaryRefusesTheTestBinary guards the second of the two doors a test can walk
// through into the real world.
//
// The first (SocketPath refusing to resolve the user's socket) has been there from the
// start. This is its mirror image, and its absence had teeth: a `go test` binary handed
// `muxd --socket X` ignores those arguments and runs the whole suite again — which spawns
// more copies of itself. Instances were found still resident hours after a run.
//
// The failure must be LOUD. A silent one is what let this go unnoticed.
func TestDaemonBinaryRefusesTheTestBinary(t *testing.T) {
	t.Setenv(EnvDaemonBin, "") // force the os.Executable path — inside a test binary

	_, err := daemonBinary()
	if err == nil {
		t.Fatalf("daemonBinary() succeeded inside a test binary; spawning it would re-run the suite")
	}
	if !strings.Contains(err.Error(), EnvDaemonBin) {
		t.Errorf("error %q does not name the variable that resolves it (%s)", err, EnvDaemonBin)
	}

	// And the override remains the sanctioned escape hatch: fixtures that build a real
	// binary must still work.
	t.Setenv(EnvDaemonBin, "/usr/bin/true")
	got, err := daemonBinary()
	if err != nil {
		t.Fatalf("daemonBinary() with an explicit override: %v", err)
	}
	if got != "/usr/bin/true" {
		t.Errorf("daemonBinary() = %q, want the override", got)
	}
}

// TestLooksLikeTestBinaryIsTheSameRuleInBothPlaces: the guard is one predicate, not two
// that can drift. If someone hardens one door, the other moves with it.
func TestLooksLikeTestBinaryIsTheSameRuleInBothPlaces(t *testing.T) {
	if !looksLikeTestBinary(os.Args[0]) {
		t.Fatalf("os.Args[0] = %q is not recognised as a test binary; both guards are inert here",
			os.Args[0])
	}
	for _, p := range []string{"/usr/local/bin/dw-terminal", "/home/u/go/bin/dw-terminal", ""} {
		if looksLikeTestBinary(p) {
			t.Errorf("looksLikeTestBinary(%q) = true; a real build would be refused", p)
		}
	}
}
