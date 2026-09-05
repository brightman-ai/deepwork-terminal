package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/brightman-ai/deepwork-terminal/muxd"
)

// runMuxd implements `dw-terminal muxd` — the resident session daemon.
//
// It runs in the foreground and logs to stdout; when the server spawns it, that stdout
// is redirected to a log file next to the socket, so a daemon that dies at startup
// leaves evidence instead of vanishing silently.
func runMuxd(args []string) error {
	fs := flag.NewFlagSet("muxd", flag.ContinueOnError)
	fs.Usage = func() {
		out := fs.Output()
		fmt.Fprint(out, "dw-terminal muxd — the resident terminal session daemon.\n\n"+
			"It owns every PTY, so restarting (or upgrading) dw-terminal does not end your\n"+
			"sessions. You normally never run this yourself: the server starts one on demand,\n"+
			"the same way a tmux client starts the tmux server.\n\nFlags:\n")
		fs.PrintDefaults()
		fmt.Fprint(out, "\nOperating it:\n"+
			"  dw-terminal muxd --status     list sessions held by the running daemon\n"+
			"  dw-terminal muxd --restart    upgrade the daemon after installing a new build\n"+
			"                                (asks first — it ENDS every live session)\n")
	}
	socket := fs.String("socket", "", "unix socket path (default: $XDG_RUNTIME_DIR/dw-muxd/<uid>.sock)")
	idle := fs.Duration("idle-timeout", muxd.DefaultIdleTimeout,
		"exit after this long with no live sessions and no clients; 0 disables")
	historyLines := fs.Int("history-lines", muxd.DefaultHistoryLines,
		"scrollback kept per session, in lines; 0 uses the default, negative disables it")
	status := fs.Bool("status", false, "print what the running daemon holds, then exit")
	restart := fs.Bool("restart", false,
		"stop the running daemon and start a fresh one — ENDS every session it holds")
	yes := fs.Bool("yes", false, "skip the confirmation prompt for --restart")
	if err := fs.Parse(args); err != nil {
		return err
	}

	path := *socket
	if path == "" {
		p, err := muxd.SocketPath()
		if err != nil {
			return err
		}
		path = p
	}

	if *status {
		return printStatus(path)
	}
	if *restart {
		return restartDaemon(path, *yes, os.Stdin, os.Stdout)
	}

	ln, err := muxd.Listen(path)
	if err != nil {
		return err
	}
	defer ln.Close()

	idleTimeout := *idle
	if idleTimeout == 0 {
		idleTimeout = -1 // negative disables the idle watchdog
	}
	d := muxd.NewDaemon(0, idleTimeout)
	d.SetHistoryLines(*historyLines)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	fmt.Printf("dw-muxd listening on %s (idle timeout %s, scrollback %s, protocol v%d)\n",
		path, idleTimeout, historyLinesLabel(*historyLines), muxd.ProtoVersion)
	err = d.Serve(ctx, ln)
	fmt.Printf("dw-muxd exiting at %s\n", time.Now().Format(time.RFC3339))
	return err
}

// historyLinesLabel renders the scrollback setting for the startup line.
//
// The startup line is the one place an operator can see what a running daemon was configured with
// — `muxd --status` talks to a daemon that may predate the flag — so "disabled" has to be spelled
// out rather than printed as "-1", which reads like a bug.
func historyLinesLabel(n int) string {
	switch {
	case n < 0:
		return "disabled"
	case n == 0:
		return fmt.Sprintf("%d lines/session", muxd.DefaultHistoryLines)
	default:
		return fmt.Sprintf("%d lines/session", n)
	}
}

// restartDaemon is the ONLY sanctioned way to end a daemon that is holding live
// sessions, and it exists because the alternative is worse.
//
// A protocol-version mismatch — the ordinary consequence of upgrading dw-terminal while
// the old daemon is still resident — can only be resolved by restarting the daemon, and
// that ends every shell it holds. Without this command the error message would have
// nothing to point at, and the user's recourse would be to work out the pid themselves
// and kill it: no warning, no session count, no idea what they were about to lose.
//
// Hence the shape: say exactly what is running and what it costs, require an explicit
// yes, and only then act. The confirmation is skippable with --yes for scripts, never by
// default — this is the one command in the product that deliberately destroys the user's
// running work.
func restartDaemon(path string, assumeYes bool, in io.Reader, out io.Writer) error {
	info, err := muxd.Inspect(path)
	if err != nil && muxd.IsNoDaemon(err) {
		fmt.Fprintf(out, "no daemon is listening on %s; starting one.\n", path)
		return startFreshDaemon(path, out)
	}
	// A version mismatch is not a reason to stop: it is the very reason we are here, and
	// Inspect still recovered who is running. Any OTHER error means we do not understand
	// what is on the other end, and destroying it blind is not acceptable.
	var vm *muxd.VersionMismatchError
	if err != nil && !errors.As(err, &vm) {
		return fmt.Errorf("cannot determine what is listening on %s (refusing to kill it blind): %w", path, err)
	}

	fmt.Fprintf(out, "daemon on %s\n", path)
	if info.PID > 0 {
		fmt.Fprintf(out, "  pid       %d\n", info.PID)
	}
	if info.Version > 0 {
		fmt.Fprintf(out, "  protocol  v%d (this build speaks v%d)\n", info.Version, muxd.ProtoVersion)
	}
	if !info.StartedAt().IsZero() {
		fmt.Fprintf(out, "  started   %s\n", info.StartedAt().Format(time.RFC3339))
	}
	switch {
	case vm != nil && info.Version == 0:
		fmt.Fprintf(out, "  sessions  unknown — this daemon is too old to say\n")
	default:
		fmt.Fprintf(out, "  sessions  %d live\n", info.Sessions)
	}
	// The reason a restart is being asked for is usually invisible: same protocol, no
	// error, different behaviour. Say it here, where the person deciding is looking.
	if missing := info.MissingFeatures(); len(missing) > 0 {
		fmt.Fprintf(out, "  missing   %s — this daemon predates that, so terminal sizing is wrong\n",
			strings.Join(missing, ", "))
	}
	fmt.Fprintf(out, "\nRestarting ends every one of those sessions. Their shells, and anything\n"+
		"running inside them, will be terminated.\n\n")

	if info.PID <= 0 {
		return fmt.Errorf("cannot identify the daemon process on %s; stop it manually", path)
	}
	if !assumeYes {
		ok, err := confirm(in, out, "Restart the daemon? [y/N] ")
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(out, "left alone.")
			return nil
		}
	}

	if err := syscall.Kill(info.PID, syscall.SIGTERM); err != nil {
		return fmt.Errorf("stop daemon pid %d: %w", info.PID, err)
	}
	// Wait on the PROCESS, not on the socket. A socket can be answered again within
	// milliseconds by a daemon some other server auto-started, which would make a
	// successful stop look like a failure — and the retry that follows would then kill
	// that innocent fresh daemon instead.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(info.PID, 0); err != nil {
			break // gone
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := syscall.Kill(info.PID, 0); err == nil {
		return fmt.Errorf("daemon pid %d did not stop within 10s", info.PID)
	}
	fmt.Fprintf(out, "stopped pid %d.\n", info.PID)
	return startFreshDaemon(path, out)
}

// startFreshDaemon brings a daemon up through the ordinary connect-or-spawn path, so the
// restarted daemon is started exactly the way the server would have started it — one
// mechanism, not a second one that can drift.
func startFreshDaemon(path string, out io.Writer) error {
	c, err := muxd.ConnectOrSpawn(path)
	if err != nil {
		return fmt.Errorf("start a fresh daemon: %w", err)
	}
	defer c.Close()
	info, err := muxd.Inspect(path)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "started pid %d, protocol v%d.\n", info.PID, info.Version)
	return nil
}

// confirm reads one line and accepts only an explicit yes. Anything else — including a
// closed stdin, which is what a non-interactive caller has — declines.
func confirm(in io.Reader, out io.Writer, prompt string) (bool, error) {
	fmt.Fprint(out, prompt)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintln(out)
		return false, nil
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

// printStatus is the observability entry point: what does the daemon actually hold?
// Answering that without it would mean guessing from `ps`, which cannot see session
// ids, exit codes, or geometry.
func printStatus(path string) error {
	if !muxd.SocketAlive(path) {
		fmt.Printf("no daemon listening on %s\n", path)
		return nil
	}
	c, err := muxd.Connect(path)
	if err != nil {
		// An incompatible daemon still owes the operator an answer. Inspect recovers what
		// it can (pid, version, session count) precisely so `--status` does not go blind
		// at the moment something is actually wrong.
		var vm *muxd.VersionMismatchError
		if errors.As(err, &vm) {
			fmt.Printf("daemon on %s is INCOMPATIBLE with this build\n  %v\n", path, vm)
			return nil
		}
		return err
	}
	defer c.Close()
	sessions, err := c.List()
	if err != nil {
		return err
	}
	// The daemon's OWN version, not ours. Printing muxd.ProtoVersion here described the
	// binary running this command — always the newest one — so a status page whose whole
	// job is reporting the other process reported itself instead.
	peer := c.Peer()
	proto := peer.Version
	if proto == 0 {
		proto = muxd.ProtoVersion // too old to say; ours is the only number available
	}
	fmt.Printf("daemon on %s — %d session(s), protocol v%d\n", path, len(sessions), proto)
	if missing := peer.MissingFeatures(); len(missing) > 0 {
		fmt.Printf("  ⚠ this daemon predates %s — it is older than the binary you just ran,\n"+
			"    so terminal sizing is wrong until you run: dw-terminal muxd --restart\n",
			strings.Join(missing, ", "))
	}
	for _, s := range sessions {
		state := "exited"
		if s.Alive {
			state = "alive"
		}
		fmt.Printf("  %s  %-6s  pid=%-7d  %dx%d  %d viewer(s)/%d attached  meta=%dB  %s  since %s\n",
			s.ID, state, s.ShellPID, s.Cols, s.Rows, s.Viewers, s.Attached, len(s.Meta),
			scrollbackLabel(s), s.CreatedAt.Format(time.RFC3339))
	}
	return nil
}

// scrollbackLabel describes one session's history for `--status`.
//
// It exists because scrollback lives in the daemon's memory and is therefore invisible from
// everywhere else: no file to inspect, and until a client asks for a page there is nothing on the
// wire either. This line is how the memory ceiling the feature was accepted under gets checked
// against a running daemon instead of trusted.
//
// "off" and "0 lines" are printed as different things on purpose — the first says this session
// keeps no history, the second that nothing has scrolled off it yet.
func scrollbackLabel(s muxd.SessionSummary) string {
	switch {
	case !s.HistoryEnabled:
		return "scrollback=off"
	case s.HistoryBroken:
		return fmt.Sprintf("scrollback=BROKEN(%d lines held)", s.HistoryLines)
	default:
		return fmt.Sprintf("scrollback=%d lines/%dKB", s.HistoryLines, s.HistoryBytes/1024)
	}
}
