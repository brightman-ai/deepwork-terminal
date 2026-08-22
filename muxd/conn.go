package muxd

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// EnvSocketOverride lets a caller point at a specific daemon socket. Tests and
// throwaway fixtures MUST set it (together with an isolated XDG_RUNTIME_DIR) so they
// can never reach the user's real daemon — the same discipline the tmux fixtures in
// this repo already follow after a cleanup loop once killed live sessions.
const EnvSocketOverride = "DW_MUXD_SOCKET"

// dirPerm/sockPerm are the whole access-control story. The daemon performs NO
// authentication of its own: anything that can open this socket is already the same OS
// user, and that user can kill any of these processes and read any of these files
// anyway. Network authentication stays where it belongs — in the HTTP host. This is
// also why the transport is a unix socket and not loopback TCP: a TCP port cannot be
// restricted with file permissions, and it would widen the network reachable surface.
const (
	dirPerm  os.FileMode = 0o700
	sockPerm os.FileMode = 0o600
)

// SocketPath resolves the daemon socket path.
//
// Order: explicit override, then $XDG_RUNTIME_DIR (a per-user, per-boot, tmpfs
// directory — the correct home for runtime sockets), then ~/.dw-terminal as a
// fallback for systems without XDG_RUNTIME_DIR (notably macOS).
//
// It deliberately never falls back to a fixed name under /tmp: that directory is
// world-writable and shared, so a fixed name there invites both squatting and symlink
// games from any other account on the machine.
func SocketPath() (string, error) {
	if p := os.Getenv(EnvSocketOverride); p != "" {
		return p, nil
	}
	// Refuse to hand a test the user's real socket.
	//
	// Without this, any test that constructs a manager the production way would
	// connect-or-spawn against the developer's live daemon — and then clean up after
	// itself by destroying sessions. That is not hypothetical here: a cleanup loop in
	// this repo once killed the user's running tmux sessions, which is why every tmux
	// fixture now takes an isolated socket. The same hazard, one layer down, deserves a
	// structural answer rather than a convention people have to remember.
	//
	// Detecting the test binary by name (rather than importing "testing") keeps the
	// testing package out of the shipped binary.
	if looksLikeTestBinary(os.Args[0]) {
		return "", fmt.Errorf("muxd: refusing to resolve the real daemon socket inside a test binary; "+
			"set %s to an isolated path (see the fixtures in this package)", EnvSocketOverride)
	}
	return resolveSocketPath(os.Getenv("XDG_RUNTIME_DIR"), os.UserHomeDir)
}

// looksLikeTestBinary recognises a `go test` binary by name.
//
// Detecting it this way — rather than importing "testing" — keeps the testing package out
// of the shipped binary. It is deliberately shared by the two doors a test could walk
// through: resolving the real socket, and exec'ing a daemon. Guarding only one of them is
// what let a test binary get spawned AS the daemon; see daemonBinary.
func looksLikeTestBinary(p string) bool {
	return strings.HasSuffix(p, ".test") || strings.Contains(p, "/_test/")
}

// resolveSocketPath holds the resolution rule itself, separated from the test guard
// above so the rule stays directly testable (the guard would otherwise make the very
// test that checks this logic impossible to write).
func resolveSocketPath(runtimeDir string, homeFn func() (string, error)) (string, error) {
	if runtimeDir != "" {
		return filepath.Join(runtimeDir, "dw-muxd", uidTag()+".sock"), nil
	}
	home, err := homeFn()
	if err != nil {
		return "", fmt.Errorf("muxd: cannot resolve socket path: %w", err)
	}
	return filepath.Join(home, ".dw-terminal", "muxd.sock"), nil
}

// uidTag names the socket after the owning uid. XDG_RUNTIME_DIR is already per-user,
// so this is belt-and-braces for the case where someone points the override at a
// shared directory.
func uidTag() string {
	if u, err := user.Current(); err == nil && u.Uid != "" {
		return u.Uid
	}
	return fmt.Sprintf("%d", os.Getuid())
}

// Listen binds the daemon socket at path, creating its directory 0700 and the socket
// 0600.
//
// A leftover socket file from a daemon that died without cleaning up is removed only
// after we prove nothing is listening on it (a probe dial). Removing it unconditionally
// would let a second daemon evict a healthy first one and split the session set in two
// — half the sessions unreachable, with no error anywhere.
func Listen(path string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return nil, fmt.Errorf("muxd: create socket dir: %w", err)
	}
	if _, err := os.Stat(path); err == nil {
		live, perr := probe(path)
		if live {
			return nil, fmt.Errorf("muxd: another daemon is already listening on %s", path)
		}
		// Reclaim ONLY on proof that nothing is bound. A probe that failed for any other
		// reason has not established the socket is stale, and unlinking on a guess is how
		// a healthy daemon gets evicted and the session set silently splits in two.
		if !noListener(perr) {
			return nil, fmt.Errorf("muxd: cannot tell whether a daemon holds %s (refusing to "+
				"take it over on a guess): %w", path, perr)
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("muxd: remove stale socket: %w", err)
		}
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("muxd: listen %s: %w", path, err)
	}
	if err := os.Chmod(path, sockPerm); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("muxd: chmod socket: %w", err)
	}
	return ln, nil
}

// probe reports whether something is actually accepting connections at path.
func probe(path string) (bool, error) {
	c, err := net.DialTimeout("unix", path, 300*time.Millisecond)
	if err != nil {
		return false, err
	}
	_ = c.Close()
	return true, nil
}

// noListener reports that a dial failed because NOTHING IS BOUND to the path — the
// socket file is absent, or it is a leftover whose owner is gone.
//
// It is deliberately narrow, and the narrowness is the whole point. Every other dial
// failure — EMFILE in this process, EAGAIN from a peer whose accept backlog is full, a
// timeout — means "I could not find out", which is not the same answer at all. Two
// places used to treat the two as identical, and both were dangerous:
//
//   - Listen removed the socket and bound over it. A healthy daemon holding the user's
//     shells could be evicted by a probe that merely ran out of file descriptors — the
//     exact split-session-set disaster that code says it prevents.
//   - Dial reported "no daemon", which re-attach treats as proof the session is gone. A
//     busy daemon would have its live sessions marked dead.
//
// "I could not find out" must propagate as itself, so callers retry instead of concluding.
func noListener(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ENOENT) ||
		errors.Is(err, os.ErrNotExist)
}

// NoDaemonError means nothing is listening at the socket.
//
// It is a distinct type because it carries a CONCLUSION the caller cannot reach from a
// bare syscall error. Sessions exist only in daemon memory, so "no daemon" is not a
// transport hiccup worth retrying — it is definitive news that every session that daemon
// held is gone. Callers that address an existing session (attach, input, kill) can
// therefore report the truth immediately instead of retrying into a void.
type NoDaemonError struct {
	Path string
	Err  error
}

func (e *NoDaemonError) Error() string {
	return fmt.Sprintf("muxd: no daemon is listening on %s: %v", e.Path, e.Err)
}

func (e *NoDaemonError) Unwrap() error { return e.Err }

// IsNoDaemon reports whether err means "there is no daemon", at any wrapping depth.
func IsNoDaemon(err error) bool {
	var n *NoDaemonError
	return errors.As(err, &n)
}

// peerVanished reports that we reached the socket but nothing was serving it — the
// connection died at the transport level before the daemon said a word.
//
// It is deliberately NARROWER than isConnectionLost, which answers a different question
// ("is this request worth retrying?"). A timeout belongs there and must not belong here:
// a daemon too busy to complete a handshake within the deadline is still holding the
// user's sessions, and concluding "no daemon" would end their tabs over a slow moment.
func peerVanished(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ENOTCONN) || errors.Is(err, net.ErrClosed)
}

// Dial connects to the daemon and completes the version handshake. The returned
// connection is ready for control-plane messages.
//
// A version mismatch comes back as *VersionMismatchError so the caller can tell the
// user "the running daemon speaks a different protocol; restarting it ends N live
// sessions" instead of surfacing an unreadable EOF. A socket nobody answers comes back
// as *NoDaemonError, for the reason given on that type.
func Dial(path string) (net.Conn, error) {
	c, _, err := DialInfo(path)
	return c, err
}

// DialInfo is Dial plus the daemon's greeting: which pid is answering, what protocol it
// speaks, how many sessions it holds. Callers that only need a working connection use
// Dial; callers that must EXPLAIN the daemon to a user need this.
//
// The greeting is returned on the mismatch path too — that is the case it exists for.
func DialInfo(path string) (net.Conn, Hello, error) {
	c, err := net.DialTimeout("unix", path, 2*time.Second)
	if err != nil {
		// Only a proven absence becomes NoDaemonError — callers act on it as definitive
		// news that the sessions are gone. A timeout or a local fd exhaustion is "could
		// not find out"; it goes back as itself so the caller retries.
		if noListener(err) {
			return nil, Hello{}, &NoDaemonError{Path: path, Err: err}
		}
		return nil, Hello{}, fmt.Errorf("muxd: dial %s: %w", path, err)
	}
	// Bound only the handshake; an attached stream must be able to idle indefinitely,
	// so the deadline is cleared as soon as the negotiation is done.
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	info, err := ClientHandshake(c)
	if err != nil {
		// A daemon too old to introduce itself still cannot hide from the kernel: ask the
		// socket who is on the other end. Without this the upgrade message degrades to
		// "something incompatible is running", which the user cannot act on.
		var vm *VersionMismatchError
		if errors.As(err, &vm) && vm.DaemonPID == 0 {
			if pid, ok := peerPID(c); ok {
				vm.DaemonPID = pid
			}
		}
		// Connecting and then being reset before a single word is the same news as never
		// connecting: the socket file outlived the process that was bound to it, which is
		// exactly what a SIGKILLed daemon leaves behind. Reported as anything else it is an
		// opaque transport error, and the caller — which is usually a re-attach deciding
		// whether a tab is still alive — retries into a void and leaves a ghost on screen.
		if peerVanished(err) {
			err = &NoDaemonError{Path: path, Err: err}
		}
		_ = c.Close()
		return nil, info, err
	}
	_ = c.SetDeadline(time.Time{})
	return c, info, nil
}

// Inspect reports what is listening at path without holding a connection open. It
// answers even when the daemon is incompatible — the one moment the answer matters most.
func Inspect(path string) (Hello, error) {
	c, info, err := DialInfo(path)
	if c != nil {
		_ = c.Close()
	}
	if err != nil {
		var vm *VersionMismatchError
		if errors.As(err, &vm) {
			// Report what we did learn, alongside the reason it is incomplete.
			return Hello{Version: vm.Theirs, PID: vm.DaemonPID, Sessions: vm.Sessions,
				StartedUnix: vm.StartedUnix}, err
		}
		return Hello{}, err
	}
	return info, nil
}

// SocketAlive reports whether a daemon is currently listening at path. It is the
// cheap check connect-or-spawn uses before deciding to start one.
func SocketAlive(path string) bool {
	live, _ := probe(path)
	return live
}
