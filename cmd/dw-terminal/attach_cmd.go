package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	terminal "github.com/brightman-ai/deepwork-terminal"
	"github.com/brightman-ai/deepwork-terminal/muxd"
)

// escapeKey is the detach prefix: Ctrl-] (0x1d, ASCII GS).
//
// Every other key must reach the program inside the session untouched, so the escape has
// to be one almost nothing binds. Ctrl-] is telnet's escape and is unbound in bash, zsh,
// vim, emacs, and the agent TUIs this product exists to drive. The obvious alternatives
// are all taken: Ctrl-b is tmux's own prefix (and attaching to a tmux session is the
// common case here), Ctrl-a is screen's and also "beginning of line" in readline, and
// docker's Ctrl-p Ctrl-q makes readline's "previous line" pay a buffering delay on every
// single press.
const escapeKey = 0x1d

// runAttach implements `dw-terminal attach <target>` — a client for the session daemon
// that needs no HTTP server at all.
//
// Why this exists, beyond convenience. The daemon's entire premise is that sessions
// outlive the thing you use to reach them; until now the only way to reach one was the
// web server, so a broken or stopped server left live shells stranded — the premise held
// and the promise did not. This is the same relationship tmux has with its server.
//
// It also pays for itself as a SECOND IMPLEMENTATION of the client contract. A protocol
// with one client is a protocol whose unused paths are untested by construction: the
// spurious "your session exited" that the daemon used to emit on detach sat latent
// precisely because nothing ever sent MsgDetach. A second client exercises the parts the
// first one happens not to use.
func runAttach(args []string) error {
	fs := flag.NewFlagSet("attach", flag.ContinueOnError)
	fs.Usage = func() {
		out := fs.Output()
		fmt.Fprint(out, "dw-terminal attach — attach this terminal to a running session.\n\n"+
			"Usage:\n  dw-terminal attach <name-or-id>\n\n"+
			"The session keeps running when you detach, and when this process dies, and when\n"+
			"the web server is restarted — it lives in the session daemon, not here.\n\nFlags:\n")
		fs.PrintDefaults()
		fmt.Fprintf(out, "\nDetach:  Ctrl-] then d      (Ctrl-] twice sends a literal Ctrl-])\n"+
			"List:    dw-terminal ls\n")
	}
	socket := fs.String("socket", "", "unix socket path (default: $XDG_RUNTIME_DIR/dw-muxd/<uid>.sock)")
	observe := fs.Bool("observe", false,
		"watch without constraining the session's size (default: this terminal counts, and the session shrinks to fit the smallest viewer)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return errors.New("attach needs exactly one session name or id")
	}

	path, err := resolveSocket(*socket)
	if err != nil {
		return err
	}
	c, err := muxd.Connect(path)
	if err != nil {
		if muxd.IsNoDaemon(err) {
			return fmt.Errorf("no session daemon is running on %s — nothing to attach to", path)
		}
		return err
	}
	defer c.Close()

	sum, err := resolveSession(c, fs.Arg(0))
	if err != nil {
		return err
	}
	if !sum.Alive {
		return fmt.Errorf("session %s has already exited (code %d); its scrollback is still "+
			"visible in the web UI", terminal.SessionLabel(sum.Meta, sum.ID), sum.ExitCode)
	}

	return attachTo(c, sum, !*observe, os.Stdin, os.Stdout)
}

// attachTo takes its terminal as parameters rather than reading os.Stdin/os.Stdout
// directly, which is what makes the whole loop testable: a test hands it a real PTY pair
// and drives the client the way a person would. A second client is only worth its weight
// if it is itself exercised.
func attachTo(c *muxd.Client, sum muxd.SessionSummary, declareSize bool, stdin, stdout *os.File) error {
	// Raw mode FIRST, restore LAST, and unconditionally. Everything below can fail; none
	// of it may leave the user's shell without echo.
	restore, err := rawMode(stdin)
	if err != nil {
		return err
	}
	var restoreOnce sync.Once
	restoreTerm := func() { restoreOnce.Do(restore) }
	defer restoreTerm()

	// A defer does not run when the process is killed, and the promise this restore makes
	// is exactly about not leaving someone's shell without echo. `kill -TERM` on an attach
	// — or a SIGINT delivered from outside — would otherwise walk straight past it.
	fatal := make(chan os.Signal, 1)
	signal.Notify(fatal, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(fatal)
	go func() {
		if _, ok := <-fatal; ok {
			restoreTerm()
			os.Exit(1)
		}
	}()

	// Declare the size of THIS terminal unless asked to observe. The session sizes itself
	// to fit every attachment (smallest wins), so attaching from a small window shrinks it
	// for everyone — which is what a multiplexer is supposed to do, and why --observe
	// exists for the times you only want to look.
	var opts muxd.AttachOptions
	localCols, localRows, isTTY := terminalSize(stdin)
	if declareSize && isTTY {
		opts.Cols, opts.Rows = localCols, localRows
	}

	stream, err := c.Attach(sum.ID, opts)
	if err != nil {
		return err
	}
	defer stream.Close()

	// AttachAck reports the size AFTER this attachment was taken into account, so it is
	// already the grid the replay was rendered for.
	cols, rows := stream.Cols, stream.Rows
	if !declareSize && isTTY && (localCols < cols || localRows < rows) {
		fmt.Fprintf(stdout, "\r\n[observing: this terminal is %dx%d, the session is %dx%d — "+
			"output will wrap. Drop --observe to make it fit.]\r\n", localCols, localRows, cols, rows)
	}

	fmt.Fprintf(stdout, "\r\n[attached to %s — Ctrl-] d to detach]\r\n",
		terminal.SessionLabel(sum.Meta, sum.ID))

	// SIGWINCH only matters when this client owns the size.
	winch := make(chan os.Signal, 1)
	if declareSize {
		signal.Notify(winch, syscall.SIGWINCH)
		defer signal.Stop(winch)
	}

	detached := make(chan struct{})
	go forwardInput(stdin, stream, detached)

	for {
		select {
		case <-detached:
			fmt.Fprint(stdout, "\r\n[detached — the session keeps running]\r\n")
			return nil

		case <-winch:
			if cols, rows, ok := terminalSize(stdin); ok {
				// Fire-and-forget by contract: an attached connection carries no request
				// ids, so a reply cannot be correlated. See the note beside MaxFrameSize.
				_ = stream.Resize(sum.ID, cols, rows)
			}

		case ev, ok := <-stream.Events:
			if !ok {
				// The channel closes without an exit event only when the CONNECTION died.
				// Reporting success here would tell a script that a session finished
				// cleanly when the daemon actually went away mid-run.
				restoreTerm()
				fmt.Fprint(stdout, "\r\n[lost the connection to the session daemon]\r\n")
				return errors.New("the session daemon went away")
			}
			switch {
			case ev.Exit != nil:
				// Ordered with the output, so the program's final line has already been
				// written by the time this arrives — no draining, no race, no coin flip
				// over whether the answer a command just printed gets shown.
				restoreTerm()
				fmt.Fprintf(stdout, "\r\n[session exited with code %d]\r\n", *ev.Exit)
				return nil
			case ev.Resize != nil:
				// Someone else attached, detached, or resized their window, and the grid
				// moved under us. Nothing to do locally — our own terminal is our own size —
				// but say it, because a screen that suddenly reflows with no explanation
				// looks like a bug in whatever is running inside.
				fmt.Fprintf(stdout, "\r\n[session resized to %dx%d]\r\n", ev.Resize[0], ev.Resize[1])
			case ev.Gap:
				fmt.Fprint(stdout, "\r\n[output was lost — this view has a gap in it]\r\n")
			default:
				if _, werr := stdout.Write(ev.Data); werr != nil {
					return werr
				}
			}
		}
	}
}

// escapeState is the detach key state machine, kept as a pure function so the rule can be
// tested exhaustively without a terminal, a socket, or a session.
//
// It matters more than its size suggests: get it wrong in one direction and a keystroke
// silently vanishes into the multiplexer; get it wrong in the other and there is no way
// out of an attached session but killing the process.
//
// Contract: only escapeKey is ever withheld. `esc d` detaches, `esc esc` sends one literal
// escapeKey, and `esc <anything else>` passes BOTH bytes through — so no key is made
// unreachable by the prefix.
func escapeState(pending bool, in []byte) (out []byte, stillPending, detach bool) {
	out = make([]byte, 0, len(in)+1)
	for i, b := range in {
		switch {
		case pending && b == 'd':
			return out, false, true
		case pending && b == escapeKey:
			pending = false
			out = append(out, escapeKey)
		case pending:
			pending = false
			out = append(out, escapeKey, b)
		case b == escapeKey:
			pending = true
		default:
			out = append(out, b)
		}
		_ = i
	}
	return out, pending, false
}

// forwardInput pumps keystrokes into the session, watching for the detach sequence.
func forwardInput(in *os.File, stream *muxd.Stream, detached chan<- struct{}) {
	buf := make([]byte, 4096)
	pending := false
	for {
		n, rerr := in.Read(buf)
		if n > 0 {
			out, stillPending, detach := escapeState(pending, buf[:n])
			pending = stillPending
			if len(out) > 0 {
				if werr := stream.Write(out); werr != nil {
					return
				}
			}
			if detach {
				close(detached)
				return
			}
		}
		if rerr != nil {
			return
		}
	}
}

// runList implements `dw-terminal ls` — what is running, in one line each.
//
// It is separate from `muxd --status` on purpose: that command is for operating the
// daemon (which process, which protocol, is it healthy), this one is for finding a
// session to attach to. Same data, different question, and folding them would make the
// common case read like an admin tool.
func runList(args []string) error {
	fs := flag.NewFlagSet("ls", flag.ContinueOnError)
	socket := fs.String("socket", "", "unix socket path (default: $XDG_RUNTIME_DIR/dw-muxd/<uid>.sock)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	path, err := resolveSocket(*socket)
	if err != nil {
		return err
	}
	c, err := muxd.Connect(path)
	if err != nil {
		if muxd.IsNoDaemon(err) {
			fmt.Println("no sessions (the session daemon is not running)")
			return nil
		}
		return err
	}
	defer c.Close()

	sessions, err := c.List()
	if err != nil {
		return err
	}
	if len(sessions) == 0 {
		fmt.Println("no sessions")
		return nil
	}
	for _, s := range sessions {
		state := "exited"
		if s.Alive {
			state = "alive"
		}
		cwd := terminal.SessionCWD(s.Meta)
		fmt.Printf("%-8s  %-18s  %-6s  %dx%d  %d viewer(s)/%d attached  pid=%-7d  %s\n",
			s.ID[:8], truncate(terminal.SessionLabel(s.Meta, s.ID), 18), state,
			s.Cols, s.Rows, s.Viewers, s.Attached, s.ShellPID, cwd)
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func resolveSocket(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	return muxd.SocketPath()
}

// resolveSession turns what a human typed into exactly one session.
//
// Ids are 32 hex characters, which nobody is going to type, so a name or an id PREFIX is
// accepted. An ambiguous target is an error rather than a guess: attaching to the wrong
// terminal and typing into it is not a mistake that can be taken back.
func resolveSession(c *muxd.Client, target string) (muxd.SessionSummary, error) {
	sessions, err := c.List()
	if err != nil {
		return muxd.SessionSummary{}, err
	}
	var matches []muxd.SessionSummary
	for _, s := range sessions {
		if s.ID == target {
			return s, nil // an exact id is never ambiguous
		}
		if strings.HasPrefix(s.ID, target) ||
			strings.EqualFold(terminal.SessionLabel(s.Meta, s.ID), target) {
			matches = append(matches, s)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return muxd.SessionSummary{}, fmt.Errorf("no session matches %q (try `dw-terminal ls`)", target)
	default:
		var names []string
		for _, s := range matches {
			names = append(names, fmt.Sprintf("%s (%s)", terminal.SessionLabel(s.Meta, s.ID), s.ID[:8]))
		}
		return muxd.SessionSummary{}, fmt.Errorf("%q matches %d sessions: %s — be more specific",
			target, len(matches), strings.Join(names, ", "))
	}
}
