package muxd

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolatedSocket returns a socket path inside t.TempDir and points the override env at
// it. Every test in this package MUST route through here: a test that resolved the real
// SocketPath() would talk to the user's live daemon.
func isolatedSocket(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "run", "test.sock")
	t.Setenv(EnvSocketOverride, path)
	return path
}

// TestProtoSocketPathPrefersXDG documents the resolution order and, more importantly,
// pins the rule that a fixed name under /tmp is never chosen — that path is shared and
// world-writable, which invites squatting and symlink games.
func TestProtoSocketPathPrefersXDG(t *testing.T) {
	t.Run("override wins", func(t *testing.T) {
		t.Setenv(EnvSocketOverride, "/somewhere/custom.sock")
		t.Setenv("XDG_RUNTIME_DIR", "/run/user/9999")
		got, err := SocketPath()
		if err != nil {
			t.Fatalf("SocketPath: %v", err)
		}
		if got != "/somewhere/custom.sock" {
			t.Errorf("path = %q, want the override", got)
		}
	})

	// These two exercise resolveSocketPath rather than SocketPath, because SocketPath
	// deliberately refuses to answer inside a test binary (so no test can ever reach the
	// user's live daemon). The resolution RULE is what matters here and it is right there.
	t.Run("xdg runtime dir", func(t *testing.T) {
		got, err := resolveSocketPath("/run/user/9999", os.UserHomeDir)
		if err != nil {
			t.Fatalf("resolveSocketPath: %v", err)
		}
		if want := filepath.Join("/run/user/9999", "dw-muxd", uidTag()+".sock"); got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
	})

	t.Run("never a fixed name under /tmp", func(t *testing.T) {
		got, err := resolveSocketPath("", func() (string, error) { return "/home/someone", nil })
		if err != nil {
			t.Fatalf("resolveSocketPath: %v", err)
		}
		if filepath.Dir(got) == "/tmp" {
			t.Errorf("fallback resolved to %q — a fixed /tmp name is a squatting/symlink surface", got)
		}
	})

	t.Run("refuses the real socket inside a test binary", func(t *testing.T) {
		t.Setenv(EnvSocketOverride, "")
		if _, err := SocketPath(); err == nil {
			t.Error("SocketPath answered inside a test binary — a test could reach the user's live daemon")
		}
	})
}

// TestProtoListenPermissions checks the entire access-control story: the daemon does no
// auth of its own, so if these modes are wrong there is nothing else standing between
// another local account and every one of the user's terminals.
func TestProtoListenPermissions(t *testing.T) {
	path := isolatedSocket(t)
	ln, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()

	si, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if perm := si.Mode().Perm(); perm != sockPerm {
		t.Errorf("socket mode = %o, want %o", perm, sockPerm)
	}
	di, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if perm := di.Mode().Perm(); perm != dirPerm {
		t.Errorf("dir mode = %o, want %o", perm, dirPerm)
	}
}

// TestProtoListenReclaimsStaleSocket covers the daemon-crashed-without-cleanup case:
// the file is there, nothing is behind it, and a new daemon must be able to take over.
func TestProtoListenReclaimsStaleSocket(t *testing.T) {
	path := isolatedSocket(t)
	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// A plain file standing in for the corpse of a dead daemon's socket.
	if err := os.WriteFile(path, nil, sockPerm); err != nil {
		t.Fatalf("write stale file: %v", err)
	}
	ln, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen over stale socket: %v", err)
	}
	defer ln.Close()
	if !SocketAlive(path) {
		t.Error("socket not accepting connections after reclaiming a stale path")
	}
}

// TestProtoListenRefusesToEvictLiveDaemon is the other half of the stale-socket rule,
// and the more dangerous one. If a second daemon could unlink a healthy first daemon's
// socket, the session set would silently split in two: half the user's terminals would
// become unreachable with no error printed anywhere.
func TestProtoListenRefusesToEvictLiveDaemon(t *testing.T) {
	path := isolatedSocket(t)
	first, err := Listen(path)
	if err != nil {
		t.Fatalf("first Listen: %v", err)
	}
	defer first.Close()
	go func() {
		for {
			c, err := first.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()

	second, err := Listen(path)
	if err == nil {
		second.Close()
		t.Fatal("second Listen evicted a live daemon — sessions would split in two")
	}
}

// TestVersionDialHandshake proves Dial completes the negotiation, so callers get a
// connection that is already known-compatible rather than one that fails later.
func TestVersionDialHandshake(t *testing.T) {
	path := isolatedSocket(t)
	ln, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()

	srvErr := make(chan error, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			srvErr <- err
			return
		}
		defer c.Close()
		srvErr <- ServerHandshake(c, Hello{})
	}()

	conn, err := Dial(path)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	if err := <-srvErr; err != nil {
		t.Errorf("server handshake: %v", err)
	}
	if _, ok := conn.(*net.UnixConn); !ok {
		t.Errorf("conn type = %T, want *net.UnixConn", conn)
	}
}

// TestVersionDialNoDaemon: dialing a path with no daemon is a plain error, not a hang.
func TestVersionDialNoDaemon(t *testing.T) {
	path := isolatedSocket(t)
	if SocketAlive(path) {
		t.Fatal("SocketAlive true for a path with no daemon")
	}
	if _, err := Dial(path); err == nil {
		t.Error("Dial succeeded with no daemon listening")
	}
}

// TestDialLearnsDaemonPIDFromKernelWhenProtocolCannotSay is the safety net under the
// upgrade message.
//
// A daemon old enough to predate the identity-carrying greeting refuses with a bare error
// frame, so the protocol yields nothing but "incompatible". That is exactly the moment a
// user most needs to know WHICH process is holding their shells — and the kernel can
// always answer, because peer credentials are not part of the protocol and therefore
// cannot become unreadable when the protocol changes.
//
// The stand-in daemon here replies the way a pre-greeting daemon does; the assertion is
// that the pid survives anyway.
func TestDialLearnsDaemonPIDFromKernelWhenProtocolCannotSay(t *testing.T) {
	path := isolatedSocket(t)
	ln, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()

	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		if _, _, err := ReadFrame(c); err != nil {
			return
		}
		_ = WriteJSON(c, MsgError, ErrorPayload{
			Code: ErrCodeVersion,
			Msg:  "daemon speaks protocol v0, client speaks v1",
		})
	}()

	_, _, err = DialInfo(path)
	var vm *VersionMismatchError
	if !errors.As(err, &vm) {
		t.Fatalf("err = %v, want *VersionMismatchError", err)
	}
	if vm.Theirs != 0 {
		t.Fatalf("precondition: this test needs the pre-greeting path, got Theirs=%d", vm.Theirs)
	}
	// Both ends are this process, so the peer pid is knowable and exact.
	if vm.DaemonPID != os.Getpid() {
		t.Errorf("DaemonPID = %d, want %d — the upgrade message cannot name the process "+
			"holding the user's sessions", vm.DaemonPID, os.Getpid())
	}
	if !strings.Contains(vm.Error(), "muxd --restart") {
		t.Errorf("message %q does not tell the user how to resolve it", vm.Error())
	}
}

// TestInspectReportsALiveDaemon: the observability entry point answers with the daemon's
// own account of itself, so `--status` and the server's logs name one authority rather
// than inferring from ps.
func TestInspectReportsALiveDaemon(t *testing.T) {
	path := isolatedSocket(t)
	ln, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()
	d := NewDaemon(1<<12, -1) // no session is created here, so the real factory is never called
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = d.Serve(ctx, ln) }()

	info, err := Inspect(path)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if info.Version != ProtoVersion {
		t.Errorf("version = %d, want %d", info.Version, ProtoVersion)
	}
	if info.PID != os.Getpid() {
		t.Errorf("pid = %d, want %d", info.PID, os.Getpid())
	}
	if info.StartedAt().IsZero() {
		t.Error("daemon did not say when it started; an operator cannot tell a fresh daemon from a stale one")
	}
}
