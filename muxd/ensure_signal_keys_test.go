package muxd

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// The 2026-09-30 incident, twice over: a TUI died without restoring its raw terminal,
// ISIG stayed off forever (zsh never re-enables it), and every ^C/^Z/^\ in that session
// became a dead byte — characters typed fine, signals never fired. These tests pin the
// repair: it must fire exactly when the payload carries the tty's own signal byte and
// the shell itself owns the foreground, and never otherwise.

func TestCarriesSignalByte_readsTheLiveCcTable(t *testing.T) {
	term := &unix.Termios{}
	term.Cc[unix.VINTR] = 0x03
	term.Cc[unix.VSUSP] = 0x1a
	term.Cc[unix.VQUIT] = 0x1c

	assert.True(t, carriesSignalByte(term, []byte{'a', 0x03}), "VINTR byte present")
	assert.True(t, carriesSignalByte(term, []byte{0x1a}), "VSUSP byte present")
	assert.True(t, carriesSignalByte(term, []byte{0x1c}), "VQUIT byte present")
	assert.False(t, carriesSignalByte(term, []byte("plain typing")), "ordinary text never triggers")
	assert.False(t, carriesSignalByte(term, nil), "empty payload")

	// A rebound VINTR is a letter; the letter must NOT trigger. This is why the cc table
	// is read live instead of hardcoding 0x03.
	rebound := &unix.Termios{}
	rebound.Cc[unix.VINTR] = 'x'
	assert.True(t, carriesSignalByte(rebound, []byte{'x'}), "rebound VINTR still detected")
	assert.False(t, carriesSignalByte(rebound, []byte{0x03}), "old default is now just a byte")
}

// Real shell, real pty, the exact incident shape: strip ISIG behind the shell's back the
// way a crashed pager would leave it, then hand the session a ^C and require the guard to
// have restored ISIG before the byte was written.
func TestEnsureSignalKeys_restoresIsigAtIdlePrompt(t *testing.T) {
	ptmx, cmd, err := RealPTY(SpawnOptions{Argv: []string{"/bin/sh"}, Cols: 80, Rows: 24})
	require.NoError(t, err)
	t.Cleanup(func() {
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
		if ptmx != nil {
			_ = ptmx.Close()
		}
	})
	s := &Session{ID: "test-isig", pty: ptmx, cmd: cmd, alive: true}

	fd := int(ptmx.Fd())
	// The shell must be foreground for the guard to fire; wait for it to settle.
	require.Eventually(t, func() bool {
		fg, err := unix.IoctlGetInt(fd, unix.TIOCGPGRP)
		return err == nil && fg == cmd.Process.Pid
	}, 2_000_000_000, 20_000_000, "shell never became the tty's foreground group")

	stripIsig := func() {
		term, err := unix.IoctlGetTermios(fd, ioctlReadTermios)
		require.NoError(t, err)
		term.Lflag &^= unix.ISIG
		require.NoError(t, unix.IoctlSetTermios(fd, ioctlWriteTermios, term))
	}

	// Incident shape: ISIG lost, ^C arrives → repaired before the write.
	stripIsig()
	s.ensureSignalKeys([]byte{0x03})
	term, err := unix.IoctlGetTermios(fd, ioctlReadTermios)
	require.NoError(t, err)
	assert.NotZero(t, term.Lflag&unix.ISIG, "ISIG must be restored before the ^C is written")

	// Healthy terminal: nothing to do, and nothing changes.
	s.ensureSignalKeys([]byte{0x03})
	term, err = unix.IoctlGetTermios(fd, ioctlReadTermios)
	require.NoError(t, err)
	assert.NotZero(t, term.Lflag&unix.ISIG)

	// Broken terminal but ordinary typing: the guard must NOT fire — text input is not
	// its business, and a cure tied to plain keys would stomp TUIs mid-keystroke.
	stripIsig()
	s.ensureSignalKeys([]byte("hello"))
	term, err = unix.IoctlGetTermios(fd, ioctlReadTermios)
	require.NoError(t, err)
	assert.Zero(t, term.Lflag&unix.ISIG, "plain text must not trigger the repair")
}

// The patrol layer: same heal core, but gated on quiescence so it never touches a TUI
// that is mid-handshake (still drawing). Both sides of that gate are pinned here.
func TestPatrolSignalKeys_quiescenceGate(t *testing.T) {
	ptmx, cmd, err := RealPTY(SpawnOptions{Argv: []string{"/bin/sh"}, Cols: 80, Rows: 24})
	require.NoError(t, err)
	t.Cleanup(func() {
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
		if ptmx != nil {
			_ = ptmx.Close()
		}
	})
	s := &Session{ID: "test-patrol", pty: ptmx, cmd: cmd, alive: true}
	fd := int(ptmx.Fd())
	require.Eventually(t, func() bool {
		fg, err := unix.IoctlGetInt(fd, unix.TIOCGPGRP)
		return err == nil && fg == cmd.Process.Pid
	}, 2_000_000_000, 20_000_000, "shell never became the tty's foreground group")

	stripIsig := func() {
		term, err := unix.IoctlGetTermios(fd, ioctlReadTermios)
		require.NoError(t, err)
		term.Lflag &^= unix.ISIG
		require.NoError(t, unix.IoctlSetTermios(fd, ioctlWriteTermios, term))
	}

	// Recent output (a TUI mid-draw): the patrol must hold off, even with ISIG missing.
	stripIsig()
	s.lastOutputNano.Store(time.Now().UnixNano())
	assert.False(t, s.patrolSignalKeys(8*time.Second), "busy session must not be healed")
	term, err := unix.IoctlGetTermios(fd, ioctlReadTermios)
	require.NoError(t, err)
	assert.Zero(t, term.Lflag&unix.ISIG, "patrol must not stomp a possibly-live TUI")

	// Quiet for long enough (an idle prompt with residue): heal.
	s.lastOutputNano.Store(time.Now().Add(-30 * time.Second).UnixNano())
	assert.True(t, s.patrolSignalKeys(8*time.Second), "quiet residue must be healed")
	term, err = unix.IoctlGetTermios(fd, ioctlReadTermios)
	require.NoError(t, err)
	assert.NotZero(t, term.Lflag&unix.ISIG)

	// Healthy terminal: nothing to do, reported as no-heal.
	assert.False(t, s.patrolSignalKeys(0), "healthy session needs no heal")
}
