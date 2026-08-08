package agentintel

import (
	"os"
	"path/filepath"
)

// IsolateTmuxForTests points every tmux command in this process at a socket that cannot exist.
//
// ── The incident this exists to prevent ──────────────────────────────────────────────────────
// 2026-08-08, twice in one evening: a developer's own tmux server — eleven days of sessions the
// first time — took SIGSEGV while this repository's tests were running.
//
// The chain is short and entirely ours until the last step. NewServer() builds a
// TmuxStateService, which builds a TmuxProber, which opens a `tmux -C attach` CONTROL CONNECTION
// against whatever socket the environment names. With TMUX unset that is the DEFAULT socket: the
// live, in-use server the person running the tests is working in. The test binary then exits —
// normally, or killed (the second crash followed an exit code 137, where nothing gets to run) —
// and that control client disappears without completing tmux's detach handshake. tmux 3.6b's
// control_write then dereferences a control_state it never checked for NULL
// (control_notify_client_detached → control_write, `ldr x8, [x22, #0x20]` with x22 == 0) and the
// server dies, taking every session with it.
//
// The NULL dereference is tmux's bug. Handing a live server a client that can vanish without
// warning, from a TEST SUITE, is ours — and it is the half that can be ended outright.
//
// ── Why a package-level hook and not a line in each test ─────────────────────────────────────
// Because a rule every future test has to remember is a rule that gets forgotten exactly once,
// and the price of that once is somebody's entire working set. Called from TestMain in each
// package that can reach a prober, it runs before any test does and cannot be bypassed by a test
// that does not know it exists.
//
// The socket path is deliberately inside a directory that does not exist: tmux fails immediately
// and creates nothing, so a test run leaves no server, no socket and no client anywhere. It also
// avoids the substring "tmux-", which TmuxProber.isTmuxClient uses to tell a server from a
// client — a path containing it would make a real client read as a non-client (already paid for
// once, see docs/product/CONFIG.md).
func IsolateTmuxForTests() {
	// Belt: no control client is ever created in this process. This is the one that matters —
	// a connection that is never opened cannot die badly, so the crash path stops being
	// reachable rather than merely being pointed somewhere else.
	tmuxControlDisabled.Store(true)
	// Braces: and every remaining tmux command (the fork/exec fallback) targets a socket in a
	// directory that does not exist, so even that path cannot reach a real server. Two
	// independent mechanisms because this guard failing silently is what it is guarding against.
	_ = os.Setenv("TMUX", filepath.Join(os.TempDir(), "dw-isolated-no-such-dir", "test.sock"))
}
