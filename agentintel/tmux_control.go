package agentintel

// A persistent tmux control-mode connection.
//
// The prober's cost was never tmux; it was PROCESS CREATION. One topology tick spawns
// `tmux` once for the pane list and once per window for that window's tail — N+1 fork/execs,
// each connecting to the same socket, asking one question, and exiting. Measured on this
// machine: 10 spawns = 45 ms idle, and 10–20 SECONDS under load, because fork/exec is exactly
// what a saturated machine is worst at. The same 10 commands over one connection: 2 ms.
//
// Control mode (`tmux -C`) is a client that speaks a line protocol on stdin/stdout, replies
// framed by %begin/%end. tmux has shipped it for a decade; iTerm2's tmux integration is built
// on it.
//
// Two properties make it safe to attach to the user's OWN session, verified on a live server
// rather than assumed:
//
//   - A control client that never DECLARES a size does not change any window's size. Attach,
//     sit for seconds, run commands — a 200x50 window stays 200x50, even under
//     `window-size latest`. Size is imposed only by an explicit `refresh-client -C`.
//   - Commands run server-wide regardless of which session the client is attached to.
//
// So this connects as a silent observer. When a web client later wants tmux to lay a pane out
// for ITS viewport, the same connection starts declaring a size — one mechanism, not two.
//
// Everything here is best-effort by construction: if the connection cannot be made, dies, or
// answers too slowly, the caller falls back to spawning a process exactly as before. The
// fallback is the old behaviour, so the worst case of this file is the status quo.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// controlDialTimeout bounds how long we wait for the connection to greet us. Beyond it the
// caller has waited long enough for an answer it could have had from a process.
const controlDialTimeout = 2 * time.Second

// controlRedialAfter throttles reconnection. A tmux server that just went away will not be
// back within a poll interval, and retrying every tick would spawn the very processes this
// exists to avoid.
const controlRedialAfter = 5 * time.Second

var errControlUnavailable = errors.New("tmux control connection unavailable")

// tmuxControl owns one connection. Commands are serialised on it: the protocol frames replies
// in order, so two callers interleaving writes would read each other's answers.
type tmuxControl struct {
	mu       sync.Mutex
	proc     *exec.Cmd
	stdin    *os.File
	replies  chan controlReply
	dead     chan struct{}
	lastDial time.Time
	dialErr  error
}

// controlReply is one %begin…%end block: the payload lines, and whether tmux ended it with
// %end (the command succeeded) or %error.
type controlReply struct {
	lines []string
	err   error
}

// newTmuxControl builds the connection, unless this process has declared it must never open one.
//
// A nil connection is a supported state — TmuxProber.run() checks for it and falls back to
// spawning a process, which is what every call did before this existed. Tests use that: not
// "point it somewhere harmless" but "do not create it at all", because a control client that
// exists is a control client that can die badly, and that is what took down a real server twice
// (see IsolateTmuxForTests).
func newTmuxControl() *tmuxControl {
	if tmuxControlDisabled.Load() {
		return nil
	}
	return &tmuxControl{}
}

// tmuxControlDisabled is set once, before any test runs, and never in production.
var tmuxControlDisabled atomic.Bool

// run executes one tmux command over the connection, dialling if needed.
//
// It returns errControlUnavailable when the connection is not usable, which is the caller's
// signal to spawn a process instead. It deliberately does NOT fall back internally: a
// transport that silently substitutes a different mechanism hides the thing you would want to
// measure, and the metric for "how often are we paying for processes" has to be readable.
func (c *tmuxControl) run(ctx context.Context, args ...string) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensureLocked(); err != nil {
		return nil, err
	}
	if _, err := c.stdin.WriteString(strings.Join(args, " ") + "\n"); err != nil {
		c.closeLocked()
		return nil, errControlUnavailable
	}
	select {
	case reply, ok := <-c.replies:
		if !ok {
			c.closeLocked()
			return nil, errControlUnavailable
		}
		return reply.lines, reply.err
	case <-c.dead:
		c.closeLocked()
		return nil, errControlUnavailable
	case <-ctx.Done():
		// The reply may still arrive and would then be read as the NEXT command's answer.
		// Drop the connection rather than risk answering a question with someone else's reply.
		c.closeLocked()
		return nil, ctx.Err()
	}
}

// ensureLocked dials if there is no live connection, at most once per controlRedialAfter.
func (c *tmuxControl) ensureLocked() error {
	if c.proc != nil {
		return nil
	}
	if time.Since(c.lastDial) < controlRedialAfter {
		return errControlUnavailable
	}
	c.lastDial = time.Now()
	if err := c.dialLocked(); err != nil {
		c.dialErr = err
		return errControlUnavailable
	}
	return nil
}

func (c *tmuxControl) dialLocked() error {
	session, err := controlAttachTarget()
	if err != nil {
		return err
	}
	// -C without -CC: control mode, and NOT the terminal-emulating variant. No size is ever
	// declared here, which is what keeps this observer invisible to the user's layout.
	cmd := exec.Command("tmux", tmuxServerArgs("-C", "attach", "-t", session)...)
	cmd.Env = sanitizedTmuxEnv(os.Environ())
	inR, inW, err := os.Pipe()
	if err != nil {
		return err
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		inR.Close()
		inW.Close()
		return err
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = inR, outW, outW
	if err := cmd.Start(); err != nil {
		inR.Close()
		inW.Close()
		outR.Close()
		outW.Close()
		return err
	}
	inR.Close()
	outW.Close()

	c.proc, c.stdin = cmd, inW
	c.replies = make(chan controlReply, 1)
	c.dead = make(chan struct{})
	go c.read(outR, c.replies, c.dead)

	// The attach preamble is itself a %begin…%end block; consume it so the first real command
	// does not read the greeting as its own answer.
	select {
	case <-c.replies:
	case <-c.dead:
		c.closeLocked()
		return errControlUnavailable
	case <-time.After(controlDialTimeout):
		c.closeLocked()
		return errControlUnavailable
	}
	return nil
}

// read parses the control protocol until the connection ends.
//
// Only %begin…%end / %error blocks are routed to callers. Everything else is a NOTIFICATION
// (%output, %window-add, %layout-change …) — the raw material for making this event-driven —
// and is dropped for now rather than half-handled. Dropping is honest; a partial event feed
// that callers start trusting is not.
func (c *tmuxControl) read(out *os.File, replies chan<- controlReply, dead chan struct{}) {
	defer close(dead)
	defer close(replies)
	defer out.Close()

	scanner := bufio.NewScanner(out)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	var block []string
	inBlock := false
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "%begin"):
			inBlock, block = true, nil
		case inBlock && isControlFrameEnd(line, "%end"):
			replies <- controlReply{lines: block}
			inBlock, block = false, nil
		case inBlock && isControlFrameEnd(line, "%error"):
			replies <- controlReply{lines: block, err: fmt.Errorf("tmux: %s", strings.Join(block, "; "))}
			inBlock, block = false, nil
		case inBlock:
			block = append(block, line)
		}
	}
}

// isControlFrameEnd distinguishes a protocol terminator from a captured line that merely looks
// like one. tmux does not escape pane content inside a block, so a screen showing the text
// "%end" would otherwise truncate its own capture. A real terminator is the keyword plus
// exactly three fields (timestamp, command number, flags).
func isControlFrameEnd(line, keyword string) bool {
	if !strings.HasPrefix(line, keyword) {
		return false
	}
	return len(strings.Fields(strings.TrimPrefix(line, keyword))) == 3
}

// controlGracefulExit bounds how long a closing connection is given to leave on its own.
//
// Closing stdin IS the goodbye in control mode: the client reads EOF, completes tmux's detach
// handshake and exits — normally within a millisecond, so this ceiling is almost never reached.
// It is a ceiling rather than a wait: a client that has wedged must not hold the connection
// mutex, and the poll interval it sits inside is 900ms.
const controlGracefulExit = 200 * time.Millisecond

// closeLocked tears the connection down, letting the client leave on its own first.
//
// ── Why not just Kill ────────────────────────────────────────────────────────────────────────
// It used to close stdin and SIGKILL in the same breath, giving the client no chance to detach.
// A control client that vanishes mid-handshake leaves the SERVER holding a half-built client:
// the CLIENT_CONTROL flag is already set while its control_state may not be — and tmux 3.6b's
// control_write dereferences that pointer without checking it. Observed on this machine
// (2026-08-08 19:34:22): the user's tmux server took SIGSEGV inside
// control_notify_client_detached → control_write, at exactly `ldr x8, [x22, #0x20]` with x22
// NULL, and eleven days of sessions went with it.
//
// The NULL dereference is tmux's bug, not ours — a client being killed is a legal thing for a
// server to survive, and we could not reproduce the race in 100 attempts, so this is NOT a
// proven fix. It is the cheap half of the trade regardless: leaving politely costs a
// sub-millisecond wait on the normal path and removes us from the list of things that hand the
// server a client it never finished building.
func (c *tmuxControl) closeLocked() {
	if c.proc == nil {
		return
	}
	if c.stdin != nil {
		c.stdin.Close()
	}
	reaped := make(chan struct{})
	go func() {
		defer close(reaped)
		_, _ = c.proc.Process.Wait()
	}()
	select {
	case <-reaped:
	case <-time.After(controlGracefulExit):
		// It did not take the hint. Now it is a stuck process, and one of those is worse than
		// an abrupt exit.
		_ = c.proc.Process.Kill()
		<-reaped
	}
	c.proc, c.stdin = nil, nil
}

// Close tears the connection down. Safe to call on a connection that was never dialled.
func (c *tmuxControl) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closeLocked()
}

// controlAttachTarget picks a session to attach to. Any session works — commands are
// server-wide — so this takes the first one rather than inventing a session of its own: a
// session we created would show up in the user's `tmux ls`, and would have to be reaped on
// every exit path including the ones that do not run.
func controlAttachTarget() (string, error) {
	cmd := exec.Command("tmux", tmuxServerArgs("list-sessions", "-F", "#{session_name}")...)
	cmd.Env = sanitizedTmuxEnv(os.Environ())
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(out), "\n") {
		if name := strings.TrimSpace(line); name != "" {
			return name, nil
		}
	}
	return "", errControlUnavailable
}
