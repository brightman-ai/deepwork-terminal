package terminal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// This is the acceptance test for the whole point of dw-muxd, stated the way a user would
// state it:
//
//	open three tabs (one running a long job), recompile and restart the server, reload —
//	all three tabs are still there, the long job is still running, the scrollback is
//	intact, and typing works immediately.
//
// It deliberately uses REAL PTYs, a REAL spawned server process that is really killed and
// really restarted, and a REAL daemon. Substituting a fake for any of those would test
// something other than the thing that was broken: the old code passed every unit test it
// had while losing every terminal on restart.
//
// Everything runs against an isolated HOME / XDG_RUNTIME_DIR / socket, so it can never
// touch the developer's live sessions.

const (
	e2eAuthCode = "e2e-test-code"
	e2eSentinel = "DWMUX-SENTINEL"
	e2eProbeTag = "dwmux-e2e-probe"
)

type e2eEnv struct {
	t       *testing.T
	bin     string
	dir     string
	sock    string
	port    int
	baseURL string
	srv     *exec.Cmd
}

func newE2EEnv(t *testing.T) *e2eEnv {
	t.Helper()
	if _, err := exec.LookPath("/bin/bash"); err != nil {
		t.Skip("bash not available")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "dw-terminal")
	build := exec.Command("go", "build", "-o", bin, "./cmd/dw-terminal")
	var stderr bytes.Buffer
	build.Stderr = &stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build dw-terminal: %v\n%s", err, stderr.String())
	}

	for _, sub := range []string{"home", "run", "tmux"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	port := freePort(t)
	env := &e2eEnv{
		t:       t,
		bin:     bin,
		dir:     dir,
		sock:    filepath.Join(dir, "run", "muxd.sock"),
		port:    port,
		baseURL: fmt.Sprintf("http://127.0.0.1:%d", port),
	}
	t.Cleanup(env.teardown)
	return env
}

// freePort asks the kernel for an unused port and immediately releases it. Racy in
// principle, fine in practice, and far better than a hardcoded number that collides with
// whatever the developer happens to be running.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// startServer launches dw-terminal with every isolation knob set.
//
// The four env vars are not belt-and-braces: without them the server's tmux prober
// attaches to the developer's DEFAULT tmux socket (observed while writing this), and the
// daemon socket would be the developer's real one.
func (e *e2eEnv) startServer() {
	e.t.Helper()
	cmd := exec.Command(e.bin,
		"-addr", fmt.Sprintf(":%d", e.port),
		"-auth-code", e2eAuthCode,
		"-shell", "/bin/bash")
	cmd.Env = append(os.Environ(),
		"HOME="+filepath.Join(e.dir, "home"),
		"XDG_RUNTIME_DIR="+filepath.Join(e.dir, "run"),
		"TMUX="+filepath.Join(e.dir, "tmux", "fix.sock"),
		"DW_MUXD_SOCKET="+e.sock,
		"DW_CLAUDE_PROJECTS="+filepath.Join(e.dir, "home", "projects"),
	)
	// Give the server its own process group so the test can signal the GROUP, which is
	// what a terminal does on Ctrl-C. Signalling only the server pid would never exercise
	// the daemon's detachment at all (an earlier version of this test did exactly that and
	// stayed green with Setsid removed — it was measuring nothing).
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	logPath := filepath.Join(e.dir, fmt.Sprintf("server-%d.log", time.Now().UnixNano()))
	logFile, err := os.Create(logPath)
	if err != nil {
		e.t.Fatalf("create server log: %v", err)
	}
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		e.t.Fatalf("start server: %v", err)
	}
	e.srv = cmd
	go func() { _ = cmd.Wait() }()

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := e.get("/api/sessions"); err == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	body, _ := os.ReadFile(logPath)
	e.t.Fatalf("server did not become ready on %s\n--- server log ---\n%s", e.baseURL, body)
}

// killServer stops the SERVER only. The daemon must survive — that is the whole test.
//
// Two details here are load-bearing, and both were wrong in earlier versions of this
// test — each time producing a test that passed while measuring nothing:
//
//   - SIGINT, not SIGTERM: cmd/dw-terminal handles os.Interrupt only, so SIGTERM kills
//     the process outright and the graceful path never runs.
//   - the whole process GROUP, not the single pid: a terminal delivers Ctrl-C to its
//     foreground group. Signalling one pid leaves the daemon untouched no matter how it
//     was started, so it cannot tell a detached daemon from an attached one — which is
//     the single most important thing this test exists to check.
func (e *e2eEnv) killServer() {
	e.t.Helper()
	if e.srv == nil || e.srv.Process == nil {
		return
	}
	pid := e.srv.Process.Pid
	// pid == pgid: the server was started with Setpgid, so it leads its own group.
	if err := syscall.Kill(-pid, syscall.SIGINT); err != nil {
		e.t.Fatalf("signal server group: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) != nil {
			e.srv = nil
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = e.srv.Process.Kill()
	e.srv = nil
}

func (e *e2eEnv) teardown() {
	e.killServer()
	// Scoped to this test's socket — never a broad pkill, which would hit the developer's
	// own daemon and terminals.
	if out, err := exec.Command("pgrep", "-f", "muxd --socket "+e.sock).Output(); err == nil {
		for _, line := range strings.Fields(string(out)) {
			_ = exec.Command("kill", line).Run()
		}
	}
}

func (e *e2eEnv) do(method, path string, body []byte) ([]byte, int, error) {
	req, err := http.NewRequest(method, e.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("X-CLI-Auth", e2eAuthCode)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(resp.Body)
	return buf.Bytes(), resp.StatusCode, nil
}

func (e *e2eEnv) get(path string) ([]byte, error) {
	b, code, err := e.do(http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("GET %s: status %d: %s", path, code, b)
	}
	return b, nil
}

type e2eSession struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (e *e2eEnv) listSessions() []e2eSession {
	e.t.Helper()
	b, err := e.get("/api/sessions")
	if err != nil {
		e.t.Fatalf("list sessions: %v", err)
	}
	var out []e2eSession
	if err := json.Unmarshal(b, &out); err != nil {
		e.t.Fatalf("decode sessions: %v (%s)", err, b)
	}
	return out
}

func (e *e2eEnv) createSession(name string) e2eSession {
	e.t.Helper()
	body, _ := json.Marshal(map[string]string{"name": name, "cwd": e.dir})
	b, code, err := e.do(http.MethodPost, "/api/sessions", body)
	if err != nil || code != http.StatusOK && code != http.StatusCreated {
		e.t.Fatalf("create session %s: err=%v code=%d body=%s", name, err, code, b)
	}
	var s e2eSession
	if err := json.Unmarshal(b, &s); err != nil {
		e.t.Fatalf("decode created session: %v (%s)", err, b)
	}
	return s
}

func (e *e2eEnv) input(id, data string) {
	e.t.Helper()
	b, code, err := e.do(http.MethodPost, "/api/sessions/"+id+"/input", []byte(data))
	if err != nil || code >= 300 {
		e.t.Fatalf("input to %s: err=%v code=%d body=%s", id, err, code, b)
	}
}

// TestE2ESessionsSurviveServerRestart is the acceptance scenario.
func TestE2ESessionsSurviveServerRestart(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns real processes")
	}
	e := newE2EEnv(t)
	e.startServer()

	// --- three tabs, one of them running a long job -------------------------------
	var ids []string
	for i := 1; i <= 3; i++ {
		ids = append(ids, e.createSession(fmt.Sprintf("tab-%d", i)).ID)
	}
	longJob := ids[1]

	// Attach before typing, so a failure to see the echo tells us the input path is
	// broken rather than leaving us guessing later.
	pre := e.dialWS(longJob)

	sentinel := fmt.Sprintf("%s-%d", e2eSentinel, time.Now().UnixNano())
	// `exec -a` renames the process so pgrep can find it. A trailing shell COMMENT would
	// not work: comments never reach the process's argv, so the tag would be invisible to
	// pgrep even though the job was running (this test's first version failed exactly
	// that way).
	e.input(longJob, fmt.Sprintf("echo %s; bash -c 'exec -a %s sleep 900'\n", sentinel, e2eProbeTag))

	if !pre.waitFor(sentinel, 20*time.Second) {
		pre.Close(websocket.StatusNormalClosure, "")
		t.Fatalf("the shell never echoed %q — input never reached the PTY", sentinel)
	}
	pre.Close(websocket.StatusNormalClosure, "")

	// Now the long job must exist.
	probePID := 0
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if out, err := exec.Command("pgrep", "-f", e2eProbeTag).Output(); err == nil {
			if f := strings.Fields(string(out)); len(f) > 0 {
				fmt.Sscanf(f[0], "%d", &probePID)
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if probePID == 0 {
		t.Fatal("long-running job never started; nothing to prove survival with")
	}
	t.Logf("before restart: ids=%v longJobPID=%d sentinel=%s", ids, probePID, sentinel)

	before := e.listSessions()
	if len(before) != 3 {
		t.Fatalf("expected 3 sessions before restart, got %d", len(before))
	}

	// --- recompile-and-restart: kill the server, leave the daemon alone ------------
	e.killServer()
	if syscall.Kill(probePID, 0) != nil {
		t.Fatal("the long-running job died with the server — this is exactly the bug dw-muxd exists to fix")
	}
	e.startServer()

	// --- assertion 1: the same session ids came back, byte for byte ----------------
	after := e.listSessions()
	beforeIDs := idsOf(before)
	afterIDs := idsOf(after)
	if beforeIDs != afterIDs {
		t.Fatalf("session ids changed across restart:\n  before: %s\n  after:  %s", beforeIDs, afterIDs)
	}
	t.Logf("after restart: ids match: %s", afterIDs)

	// --- assertion 2: the long job is still running --------------------------------
	if err := syscall.Kill(probePID, 0); err != nil {
		t.Fatalf("long-running job (pid %d) is gone after restart: %v", probePID, err)
	}
	t.Logf("long job pid %d still alive", probePID)

	// --- assertions 3 & 4: what the user sees on reload -----------------------------
	// One WebSocket, because that IS the reload path: the browser connects and the server
	// replays the scrollback into it. After a restart the server holds no buffer of its
	// own, so every byte of that replay came back from the daemon over the protocol.
	ws := e.dialWS(longJob)
	defer ws.Close(websocket.StatusNormalClosure, "done")

	if !ws.waitFor(sentinel, 20*time.Second) {
		t.Fatalf("replayed scrollback did not contain the pre-restart sentinel %q", sentinel)
	}
	t.Logf("replayed scrollback contains pre-restart sentinel %q", sentinel)

	after4 := fmt.Sprintf("DWMUX-AFTER-%d", time.Now().UnixNano())
	// Ctrl-C first: the shell is sitting in `sleep 900`.
	e.input(longJob, "\x03")
	time.Sleep(300 * time.Millisecond)
	e.input(longJob, "echo "+after4+"\n")
	if !ws.waitFor(after4, 20*time.Second) {
		t.Fatalf("input sent after the restart never produced output (%q)", after4)
	}
	t.Logf("post-restart input round-tripped: %q", after4)
}

func idsOf(list []e2eSession) string {
	out := make([]string, 0, len(list))
	for _, s := range list {
		out = append(out, s.ID)
	}
	return strings.Join(out, ",")
}

// e2eWS is a WebSocket attached to one session, accumulating everything it receives.
type e2eWS struct {
	t    *testing.T
	conn *websocket.Conn
	seen strings.Builder
}

// dialWS opens the same WebSocket the browser opens on page load.
func (e *e2eEnv) dialWS(id string) *e2eWS {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	url := strings.Replace(e.baseURL, "http://", "ws://", 1) +
		"/api/sessions/" + id + "/ws?auth=" + e2eAuthCode
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		e.t.Fatalf("dial ws for %s: %v", id, err)
	}
	conn.SetReadLimit(16 << 20)
	return &e2eWS{t: e.t, conn: conn}
}

func (w *e2eWS) Close(code websocket.StatusCode, reason string) {
	_ = w.conn.Close(code, reason)
}

// waitFor reads frames until want shows up or the deadline passes.
func (w *e2eWS) waitFor(want string, timeout time.Duration) bool {
	w.t.Helper()
	deadline := time.Now().Add(timeout)
	if strings.Contains(w.seen.String(), want) {
		return true
	}
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		_, data, err := w.conn.Read(ctx)
		cancel()
		if err != nil {
			break
		}
		w.seen.Write(data)
		if strings.Contains(w.seen.String(), want) {
			return true
		}
	}
	got := w.seen.String()
	if len(got) > 400 {
		got = got[len(got)-400:]
	}
	w.t.Logf("ws never saw %q; last bytes: %q", want, got)
	return false
}
