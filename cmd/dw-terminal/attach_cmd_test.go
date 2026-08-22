package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"

	"github.com/brightman-ai/deepwork-terminal/muxd"
)

// TestEscapeStatePassesEverythingButItsOwnPrefix is the contract for the detach key, and
// the reason it is exhaustive is that both failure directions are bad in a way the user
// cannot work around: a keystroke that vanishes into the multiplexer is a bug they will
// blame on the program inside, and a detach that cannot be typed leaves killing the
// process as the only exit.
func TestEscapeStatePassesEverythingButItsOwnPrefix(t *testing.T) {
	esc := byte(escapeKey)

	cases := []struct {
		name    string
		pending bool
		in      []byte
		out     []byte
		still   bool
		detach  bool
	}{
		{"ordinary bytes pass through untouched", false,
			[]byte("ls -la\r"), []byte("ls -la\r"), false, false},
		{"control characters are not special", false,
			[]byte{0x03, 0x04, 0x1a, 0x7f}, []byte{0x03, 0x04, 0x1a, 0x7f}, false, false},
		{"a lone escape is withheld, waiting for the next key", false,
			[]byte{esc}, []byte{}, true, false},
		{"escape then d detaches", true,
			[]byte{'d'}, []byte{}, false, true},
		{"escape then escape sends one literal escape", true,
			[]byte{esc}, []byte{esc}, false, false},
		{"escape then an unrelated key sends BOTH — no key becomes unreachable", true,
			[]byte{'x'}, []byte{esc, 'x'}, false, false},
		{"the sequence may arrive in one read", false,
			[]byte{'a', esc, 'd'}, []byte{'a'}, false, true},
		{"input after the detach in the same read is dropped, not sent to a session we left", false,
			[]byte{'a', esc, 'd', 'b', 'c'}, []byte{'a'}, false, true},
		{"escape at the end of a read stays pending across reads", false,
			[]byte{'a', esc}, []byte{'a'}, true, false},
		{"a literal escape mid-stream still delivers the surrounding bytes", true,
			[]byte{esc, 'z'}, []byte{esc, 'z'}, false, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, still, detach := escapeState(tc.pending, tc.in)
			if !bytes.Equal(out, tc.out) {
				t.Errorf("out = %v, want %v", out, tc.out)
			}
			if still != tc.still {
				t.Errorf("stillPending = %v, want %v", still, tc.still)
			}
			if detach != tc.detach {
				t.Errorf("detach = %v, want %v", detach, tc.detach)
			}
		})
	}
}

// TestEscapeStateNeverLosesAByte is the property the table above checks case by case,
// asserted over every possible byte: for any input containing no escape key, the output is
// the input. A multiplexer that eats one key in a thousand is worse than one that eats
// none, and far harder to report.
func TestEscapeStateNeverLosesAByte(t *testing.T) {
	for b := 0; b < 256; b++ {
		if byte(b) == escapeKey {
			continue
		}
		in := []byte{byte(b)}
		out, still, detach := escapeState(false, in)
		if !bytes.Equal(out, in) || still || detach {
			t.Fatalf("byte %#x: out=%v pending=%v detach=%v — a key was altered or swallowed",
				b, out, still, detach)
		}
		// And it survives the prefix too, as the literal two-byte form.
		out, still, detach = escapeState(true, in)
		if !bytes.Equal(out, []byte{escapeKey, byte(b)}) || still || detach {
			if byte(b) == 'd' {
				continue // 'd' is the one documented command
			}
			t.Fatalf("esc + %#x: out=%v — the prefix made this key unreachable", b, out)
		}
	}
}

// TestAttachRoundTripOverARealPTY drives the CLI the way a person does: a real pseudo
// terminal on one side, a real daemon and a real shell on the other.
//
// It is the acceptance test for the claim this command is built on — that a session can be
// reached with no HTTP server anywhere in the picture — and it is also the cheapest way to
// keep the client contract honest. A protocol with a single client is one whose unused
// paths are untested by construction; the daemon's spurious "your session exited" on
// detach survived for exactly that reason.
func TestAttachRoundTripOverARealPTY(t *testing.T) {
	path := isolatedSocket(t)
	t.Setenv(muxd.EnvDaemonBin, buildSelf(t))

	c, err := muxd.ConnectOrSpawn(path)
	if err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	defer c.Close()
	t.Cleanup(func() { stopDaemon(path) })

	id, _, err := c.Create(muxd.CreateReq{
		Argv: []string{"/bin/sh"}, Cwd: t.TempDir(), Cols: 80, Rows: 24,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sessions, err := c.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var sum muxd.SessionSummary
	for _, s := range sessions {
		if s.ID == id {
			sum = s
		}
	}
	if sum.ID == "" {
		t.Fatal("the session just created is not in the list")
	}

	// A real terminal for the client to live in.
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatalf("open pty: %v", err)
	}
	defer ptmx.Close()
	defer tty.Close()

	done := make(chan error, 1)
	go func() { done <- attachTo(c, sum, false, tty, tty) }()

	// Type a command and demand the shell's output back through the client.
	marker := "ATTACH-MARKER-7391"
	waitForPTY(t, ptmx, "attached to", 10*time.Second) // banner first, so the pump is live
	if _, err := ptmx.Write([]byte("echo " + marker + "\n")); err != nil {
		t.Fatalf("write to pty: %v", err)
	}
	waitForPTY(t, ptmx, marker, 15*time.Second)

	// Detach with the documented sequence, and require the session to survive it.
	if _, err := ptmx.Write([]byte{escapeKey, 'd'}); err != nil {
		t.Fatalf("write detach: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("attach returned %v, want a clean detach", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Ctrl-] d did not detach — the only way out would be killing the process")
	}

	after, err := c.List()
	if err != nil {
		t.Fatalf("List after detach: %v", err)
	}
	found := false
	for _, s := range after {
		if s.ID == id {
			found = true
			if !s.Alive {
				t.Error("the session died when the client detached; detaching must never end it")
			}
		}
	}
	if !found {
		t.Error("the session vanished from the daemon on detach")
	}
}

// waitForPTY reads until want appears or the budget runs out, reporting what it did see.
func waitForPTY(t *testing.T, r io.Reader, want string, budget time.Duration) {
	t.Helper()
	deadline := time.Now().Add(budget)
	var seen []byte
	buf := make([]byte, 4096)
	for time.Now().Before(deadline) {
		if f, ok := r.(*os.File); ok {
			_ = f.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		}
		n, err := r.Read(buf)
		if n > 0 {
			seen = append(seen, buf[:n]...)
			if strings.Contains(string(seen), want) {
				return
			}
		}
		if err != nil && n == 0 {
			continue
		}
	}
	t.Fatalf("never saw %q within %s; got:\n%s", want, budget, seen)
}
