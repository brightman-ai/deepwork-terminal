package terminal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"github.com/creack/pty"
	"github.com/google/uuid"

	"github.com/brightman-ai/kit/log"
	"github.com/brightman-ai/kit/obs"
)

var logger = log.Module("terminal")

// PTYStartOptions describes how a PTY-backed process should be started.
type PTYStartOptions struct {
	Shell string
	CWD   string
}

// CreateOptions describes the product-level terminal session metadata and runtime
// options supplied by the WebUI.
type CreateOptions struct {
	Name   string
	Title  string
	Engine string
	Shell  string
	CWD    string
}

// PTYFactory creates a PTY-backed process. Returns the master side file descriptor,
// the command (may be nil for mock implementations), and an error.
// This abstraction allows testing without fork/exec.
type PTYFactory func(opts PTYStartOptions) (master *os.File, cmd *exec.Cmd, err error)

// DefaultPTYFactory creates a real PTY. Tries independent process group (Setpgid)
// for DDC-01 SIGHUP isolation; falls back gracefully in restricted environments
// (containers, seccomp) where Setpgid is denied.
//
// The shell string may carry args (config.go documents e.g. "/bin/bash --login",
// and "tmux attach -t x" is a common case), so we tokenize it shell-words style
// before exec rather than passing the whole string as a single program path.
func DefaultPTYFactory(opts PTYStartOptions) (*os.File, *exec.Cmd, error) {
	prog, args := splitShell(opts.Shell)
	newCmd := func() *exec.Cmd {
		c := exec.Command(prog, args...)
		if opts.CWD != "" {
			c.Dir = opts.CWD
		}
		c.Env = ptyEnv(os.Environ())
		return c
	}
	cmd := newCmd()
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
	ptmx, err := pty.Start(cmd)
	if err != nil {
		// Fallback: retry without Setpgid (restricted environment).
		cmd = newCmd()
		ptmx, err = pty.Start(cmd)
		if err != nil {
			return nil, nil, err
		}
	}
	return ptmx, cmd, nil
}

func ptyEnv(env []string) []string {
	out := make([]string, 0, len(env)+2)
	for _, item := range env {
		if strings.HasPrefix(item, "TERM=") || strings.HasPrefix(item, "COLORTERM=") {
			continue
		}
		out = append(out, item)
	}
	out = append(out, "TERM=xterm-256color", "COLORTERM=truecolor")
	return out
}

// splitShell tokenizes a shell command string into program + args, honouring
// single and double quotes and backslash escapes (POSIX shell-words style). A
// single bare token (e.g. "/bin/zsh") yields that token and no args, so existing
// single-shell behaviour is unchanged. An empty/whitespace-only string yields an
// empty program, which exec rejects with a clear error — the same as before.
func splitShell(s string) (prog string, args []string) {
	var (
		tokens  []string
		cur     strings.Builder
		inToken bool
		quote   rune // 0, '\'' or '"'
	)
	flush := func() {
		if inToken {
			tokens = append(tokens, cur.String())
			cur.Reset()
			inToken = false
		}
	}
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch {
		case quote == '\'':
			if c == '\'' {
				quote = 0
			} else {
				cur.WriteRune(c)
			}
		case quote == '"':
			if c == '"' {
				quote = 0
			} else if c == '\\' && i+1 < len(rs) && (rs[i+1] == '"' || rs[i+1] == '\\') {
				i++
				cur.WriteRune(rs[i])
			} else {
				cur.WriteRune(c)
			}
		case c == '\'' || c == '"':
			quote = c
			inToken = true
		case c == '\\' && i+1 < len(rs):
			i++
			cur.WriteRune(rs[i])
			inToken = true
		case c == ' ' || c == '\t' || c == '\n':
			flush()
		default:
			cur.WriteRune(c)
			inToken = true
		}
	}
	flush()
	if len(tokens) == 0 {
		return "", nil
	}
	return tokens[0], tokens[1:]
}

// activeConnEntry tracks the active WS connection for a session (BUG-3 preemption).
type activeConnEntry struct {
	conn   *websocket.Conn
	cancel context.CancelFunc
}

// SessionManager manages terminal sessions with PTY processes.
// All state is held in memory (IR-03: no DB, no persistence).
// [Ref: T5-B3, CAP-session-lifecycle S2, DDC-11]
type SessionManager struct {
	sessions     sync.Map // map[string]*Session
	activeConns  sync.Map // map[string]*activeConnEntry — one per session (BUG-3)
	bufferSize   int
	defaultShell string
	ptyFactory   PTYFactory
}

// NewSessionManager creates a new SessionManager.
func NewSessionManager(bufferSize int, defaultShell string) *SessionManager {
	if bufferSize <= 0 {
		bufferSize = DefaultBufferCapacity
	}
	if defaultShell == "" {
		if shell := os.Getenv("SHELL"); shell != "" {
			defaultShell = shell
		} else {
			defaultShell = "/bin/bash"
		}
	}
	return &SessionManager{
		bufferSize:   bufferSize,
		defaultShell: defaultShell,
		ptyFactory:   DefaultPTYFactory,
	}
}

// NewSessionManagerWithFactory creates a SessionManager with a custom PTY factory (for testing).
func NewSessionManagerWithFactory(bufferSize int, defaultShell string, factory PTYFactory) *SessionManager {
	sm := NewSessionManager(bufferSize, defaultShell)
	sm.ptyFactory = factory
	return sm
}

// Create creates a new terminal session with a PTY process.
// [Ref: T5-B3, T5-B4.M1, CAP-session-lifecycle S2]
func (m *SessionManager) Create(name string) (*Session, error) {
	return m.CreateWithOptions(CreateOptions{Name: name})
}

// CreateWithOptions creates a new terminal session with product metadata.
func (m *SessionManager) CreateWithOptions(opts CreateOptions) (*Session, error) {
	id := uuid.New().String()
	name := opts.Name
	if name == "" {
		name = opts.Title
	}
	if name == "" {
		name = time.Now().Format("0102-1504") // MMdd-HHmm format
	}

	shellPath := opts.Shell
	if shellPath == "" {
		shellPath = m.defaultShell
	}
	engine := opts.Engine
	if engine == "" {
		engine = "shell"
	}
	cwd := opts.CWD
	// Expand ~ to user home directory.
	if cwd == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			cwd = home
		}
	} else if strings.HasPrefix(cwd, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			cwd = filepath.Join(home, cwd[2:])
		}
	}
	// Apply default CWD when none specified.
	if cwd == "" {
		if home, err := os.UserHomeDir(); err == nil {
			cwd = filepath.Join(home, "code", "work")
		}
	}
	// Create directory if it doesn't exist.
	if cwd != "" {
		if _, err := os.Stat(cwd); os.IsNotExist(err) {
			if mkErr := os.MkdirAll(cwd, 0755); mkErr != nil {
				return nil, fmt.Errorf("cannot create cwd: %w", mkErr)
			}
		}
	}
	// Validate it is a directory.
	if cwd != "" {
		stat, err := os.Stat(cwd)
		if err != nil {
			return nil, fmt.Errorf("cwd unavailable: %w", err)
		}
		if !stat.IsDir() {
			return nil, fmt.Errorf("cwd is not a directory: %s", cwd)
		}
	}

	ptmx, cmd, err := m.ptyFactory(PTYStartOptions{Shell: shellPath, CWD: cwd})
	if err != nil {
		return nil, fmt.Errorf("start pty: %w", err)
	}

	now := time.Now()
	sess := &Session{
		ID:          id,
		Name:        name,
		Title:       opts.Title,
		Engine:      engine,
		CWD:         cwd,
		ShellPath:   shellPath,
		PTY:         ptmx,
		Cmd:         cmd,
		Buffer:      NewRingBuffer(m.bufferSize),
		Status:      StatusRunning,
		CreatedAt:   now,
		LastActive:  now,
		subscribers: make(map[string]chan []byte),
		done:        make(chan struct{}),
	}

	m.sessions.Store(id, sess)
	terminalSpawnTotal.Inc()
	terminalActive.Add(1)

	// BUG-6: Detect tmux in child process environment.
	if detectTmux(cmd) {
		sess.mu.Lock()
		sess.TmuxDetected = true
		sess.mu.Unlock()
		logger.Info("tmux detected in session", "id", id)
		terminalLogger.Info(obs.WithStage(context.Background(), stgTerminalSpawn), "tmux detected in session", "session_id", id)
	}

	// Start read loop goroutine (Step 1.3).
	// Pass PTY file directly to avoid data race with Destroy setting sess.PTY = nil.
	go m.readLoop(sess, ptmx)

	logger.Info("session created",
		"id", id,
		"name", name,
		"title", opts.Title,
		"engine", engine,
		"cwd", cwd,
		"shell", shellPath)
	terminalLogger.Info(obs.WithStage(context.Background(), stgTerminalSpawn), "session created",
		"session_id", id,
		"name", name,
		"title", opts.Title,
		"engine", engine,
		"cwd", cwd,
		"shell", shellPath)

	return sess, nil
}

// Get returns a session by ID or an error if not found.
func (m *SessionManager) Get(id string) (*Session, error) {
	v, ok := m.sessions.Load(id)
	if !ok {
		return nil, fmt.Errorf("session not found: %s", id)
	}
	return v.(*Session), nil
}

// List returns all sessions.
func (m *SessionManager) List() []*Session {
	var result []*Session
	m.sessions.Range(func(_, value any) bool {
		result = append(result, value.(*Session))
		return true
	})
	return result
}

// Destroy terminates a session's PTY process and removes it from the manager.
// [Ref: CAP-session-lifecycle S2]
func (m *SessionManager) Destroy(id string) error {
	v, ok := m.sessions.LoadAndDelete(id)
	if !ok {
		return fmt.Errorf("session not found: %s", id)
	}
	sess := v.(*Session)

	// Kill the process (group kill if Setpgid was used, otherwise direct kill).
	if sess.Cmd != nil && sess.Cmd.Process != nil {
		pgid, err := syscall.Getpgid(sess.Cmd.Process.Pid)
		if err == nil && pgid == sess.Cmd.Process.Pid {
			_ = syscall.Kill(-pgid, syscall.SIGKILL) // process group kill
		} else {
			_ = sess.Cmd.Process.Kill() // direct kill (fallback)
		}
	}

	// Close PTY under lock to prevent races with Setsize and other accessors.
	sess.mu.Lock()
	ptyFile := sess.PTY
	sess.PTY = nil
	sess.mu.Unlock()
	if ptyFile != nil {
		_ = ptyFile.Close()
	}

	// Wait for process to avoid zombies.
	if sess.Cmd != nil {
		_ = sess.Cmd.Wait()
	}

	logger.Info("session destroyed", "id", id)
	terminalActive.Sub(1)
	terminalDuration.Observe(time.Since(sess.CreatedAt).Seconds())
	clearTerminalInputTracker(id)
	terminalLogger.Info(obs.WithStage(context.Background(), stgTerminalTerminate), "session destroyed",
		"session_id", id,
		"duration_ms", time.Since(sess.CreatedAt).Milliseconds())
	return nil
}

// Subscribe adds a subscriber channel for receiving PTY output.
// Returns a channel and an unsubscribe function.
func (m *SessionManager) Subscribe(sess *Session, subID string) (<-chan []byte, func()) {
	ch := make(chan []byte, 256)
	sess.subMu.Lock()
	sess.subscribers[subID] = ch
	sess.subMu.Unlock()

	unsub := func() {
		sess.subMu.Lock()
		delete(sess.subscribers, subID)
		close(ch)
		sess.subMu.Unlock()
	}
	return ch, unsub
}

// SetActiveConn registers a new active WS connection for a session, preempting any existing one.
// BUG-3: Only one WS connection per session is allowed at a time.
func (m *SessionManager) SetActiveConn(sessionID string, conn *websocket.Conn, cancel context.CancelFunc) {
	newEntry := &activeConnEntry{conn: conn, cancel: cancel}

	if prev, loaded := m.activeConns.Swap(sessionID, newEntry); loaded {
		terminalWSPreemptionsTotal.Inc()
		old := prev.(*activeConnEntry)
		// Send preempted message to old connection before closing.
		payload, _ := json.Marshal(PreemptedPayload{Message: "Another client connected"})
		msg, _ := json.Marshal(WSControlMessage{
			Type:    MsgTypePreempted,
			Payload: payload,
		})
		writeCtx, writeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = old.conn.Write(writeCtx, websocket.MessageText, msg)
		writeCancel()

		// Cancel the old connection's context and close it.
		old.cancel()
		old.conn.Close(websocket.StatusPolicyViolation, "preempted by new connection")
		logger.Info("preempted existing WS connection", "sessionId", sessionID)
		terminalLogger.Info(obs.WithStage(context.Background(), stgTerminalAttach), "cli ws preempted",
			"session_id", sessionID)
	}
}

// ClearActiveConn removes the active connection entry for a session if it matches the given conn.
func (m *SessionManager) ClearActiveConn(sessionID string, conn *websocket.Conn) {
	if v, ok := m.activeConns.Load(sessionID); ok {
		entry := v.(*activeConnEntry)
		if entry.conn == conn {
			m.activeConns.Delete(sessionID)
		}
	}
}

// CloseAll terminates all sessions. Called during server shutdown.
func (m *SessionManager) CloseAll() error {
	m.sessions.Range(func(key, _ any) bool {
		_ = m.Destroy(key.(string))
		return true
	})
	return nil
}

// DestroyAll terminates all sessions. Kept for compatibility.
func (m *SessionManager) DestroyAll() {
	_ = m.CloseAll()
}

// detectTmux checks if the PTY child process is running inside tmux by reading
// /proc/{pid}/environ (Linux only). Returns true if TMUX= is found.
// [Ref: BUG-6, DDC-13]
func detectTmux(cmd *exec.Cmd) bool {
	if runtime.GOOS != "linux" || cmd == nil || cmd.Process == nil {
		return false
	}
	pid := cmd.Process.Pid
	environPath := fmt.Sprintf("/proc/%d/environ", pid)
	data, err := os.ReadFile(environPath)
	if err != nil {
		// Cannot read environ (permissions, non-Linux) — assume no tmux.
		return false
	}
	// /proc/{pid}/environ entries are null-separated.
	for _, entry := range bytes.Split(data, []byte{0}) {
		if bytes.HasPrefix(entry, []byte("TMUX=")) {
			return true
		}
	}
	return false
}

// readLoop reads from the PTY and writes to the RingBuffer + active subscribers.
// Detects shell EOF → sets session status to "exited".
// [Ref: CAP-terminal-io S2-3, DDC-01]
func (m *SessionManager) readLoop(sess *Session, ptyFile *os.File) {
	buf := make([]byte, 32*1024)
	outputLogCtx := obs.WithStage(context.Background(), stgTerminalOutput)
	defer func() {
		sess.doneOnce.Do(func() {
			exitCode := 0
			if sess.Cmd != nil && sess.Cmd.ProcessState != nil {
				exitCode = sess.Cmd.ProcessState.ExitCode()
			}
			sess.mu.Lock()
			sess.Status = StatusExited
			sess.exitCode = exitCode
			sess.mu.Unlock()

			close(sess.done)
			logger.Info("session exited", "id", sess.ID, "exitCode", exitCode)
		})
	}()

	for {
		n, err := ptyFile.Read(buf)
		if n > 0 {
			data := make([]byte, n)
			copy(data, buf[:n])

			observeTerminalOutput(outputLogCtx, sess.ID, data)
			sess.Buffer.Write(data)

			sess.mu.Lock()
			sess.LastActive = time.Now()
			sess.mu.Unlock()

			sess.subMu.RLock()
			for _, ch := range sess.subscribers {
				select {
				case ch <- data:
				default:
				}
			}
			sess.subMu.RUnlock()
		}
		if err != nil {
			if err != io.EOF {
				logger.Debug("pty read error", "id", sess.ID, "error", err)
			}
			if sess.Cmd != nil {
				_ = sess.Cmd.Wait()
			}
			return
		}
	}
}
