package terminal

import (
	"os"
	"testing"

	"github.com/brightman-ai/deepwork-terminal/agentintel"
)

// TestMain isolates this binary from any real tmux server before a single test runs.
//
// NewServer() builds a TmuxStateService — so EVERY test that stands up a server, which is most
// of them, was opening a `tmux -C attach` control connection against the developer's own live
// tmux. Dropping that client without a detach handshake is what crashed a real server twice on
// 2026-08-08, the second time following an exit code 137 where no cleanup could run at all.
//
// The rule lives here rather than in each test because a rule that has to be remembered gets
// forgotten exactly once, and that once costs somebody their whole working set. See
// agentintel.IsolateTmuxForTests.
func TestMain(m *testing.M) {
	agentintel.IsolateTmuxForTests()
	os.Exit(m.Run())
}
