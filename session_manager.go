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
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"github.com/creack/pty"
	"github.com/google/uuid"

	"github.com/brightman-ai/deepwork-terminal/ansisignal"
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
		// cmd.Environ() 而非 os.Environ()：Go 只在 Env 仍为 nil 时才按 Dir 同步 `PWD`，显式赋值
		// 会把那层同步关掉，shell 的 `$PWD` 就会停在宿主的启动目录上。必须在设完 Dir 之后取。
		c.Env = ptyEnv(c.Environ())
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

// agentSessionMarkers —— 必须**摘掉**、不能传给 PTY 子进程的环境变量。
//
// ── 这是在修一个真实事故，不是防御性洁癖 ────────────────────────────────────────────────────
// 终端宿主（dw-host）如果**自己**是从某个 agent CLI 的 session 里被拉起来的（比如有人在 Claude
// Code 里敲了一句 `run_cli.sh`），那一刻它的进程环境就永久沾上了那个 session 的身份标记。宿主是
// 常驻的、几个月不重启，于是它之后开出的**每一个**终端面板 → 每一个 shell → 你在面板里敲的每一个
// `claude`，都顺着进程树继承了这份标记。
//
// 后果是静默的：Claude Code 用 `CLAUDE_CODE_CHILD_SESSION` 识别「我是被另一个 Claude Code 当子进程
// 拉起来的嵌套 session」，嵌套 session 默认**不落盘 transcript**（避免和父 session 抢同一个文件）。
// 于是使用者在 webui 里开的每一个 claude 都不写 transcript —— 没有报错，没有提示，只是记录不见了。
// 实测（2026-08-10 那台常驻 dw-host，8/10 19:57 启动后再没重启）：进程环境里坐着
// `CLAUDE_CODE_CHILD_SESSION=1` + `CLAUDE_CODE_SESSION_ID=…`，四层进程全带着。
//
// ── 为什么摘在这里，而不是让宿主开机时洗自己 ─────────────────────────────────────────────────
// 泄漏链只有一条：宿主 → PTY → 里面跑的 agent。宿主自己环境脏不害人（它不 spawn agent）。摘在这里
// 是**库级**的：任何把终端功能当库用的宿主（dw-host 就是）都自动生效，不需要每个宿主各自记得洗一遍；
// 而且只洗「交给子进程的那一份」，宿主自己的环境原样留着 —— 那份脏环境恰恰是当初查出根因的凭据。
//
// ── 名单为什么是逐条列的，而不是按 `CLAUDE_CODE_*` 前缀一刀切 ─────────────────────────────────
// 同一个前缀下**身份**和**配置**混在一起，一刀切会把使用者真正想要的设置也摘掉：
//   摘：身份/血缘标记（我是谁的子进程、我的 session id、我在什么沙箱里）——继承下来一定是错的。
//   留：配置（CLAUDE_CODE_MAX_OUTPUT_TOKENS、CODEX_HOME…）与凭据（CODEX_API_KEY、ACCESS_TOKEN…）
//       ——继承下来正是使用者要的，摘了会让面板里的 agent 变得没配置、甚至登不上。
// 新增条目前先问一句：这个变量回答的是「我是谁的孩子」还是「我该怎么工作」。前者才进这张表。
var agentSessionMarkers = []string{
	// Claude Code —— 元凶就在这一组（本机 + 事故机双向实证）。
	"CLAUDECODE",                // "你正跑在 Claude Code 里"
	"CLAUDE_CODE_CHILD_SESSION", // 嵌套标记：直接关掉 transcript 落盘
	"CLAUDE_CODE_SESSION_ID",
	"CLAUDE_CODE_ENTRYPOINT",
	"CLAUDE_CODE_EXECPATH",
	"CLAUDE_PID",         // 父 session 的进程号
	"CLAUDE_PLUGIN_DATA", // 父 session 的插件态
	// Codex —— 变量名取自 codex 主二进制里的字符串表 + 本机实测，不是猜的。
	"CODEX_THREAD_ID",            // 线程/会话身份
	"CODEX_COMPANION_SESSION_ID", // codex companion 的 session 身份
}

// 下面这几个**刻意不摘**，尽管它们的名字看起来同族 —— 记在这里，免得下一个人"顺手补全"：
//
//   - `CLAUDE_EFFORT`：这是**配置**（推理档位），不是血缘。摘掉它等于替使用者悄悄改设置。
//     判据就是本文件上面那句：它回答的是「我该怎么工作」，不是「我是谁的孩子」。
//   - `CODEX_SANDBOX` / `CODEX_SANDBOX_NETWORK_DISABLED`：这是**执行边界的事实**，不是身份。
//     如果 PTY 其实仍在同一个 OS 沙箱里（我们无从判断），摘掉它只会让子 agent 对自己有没有网络
//     做出错误判断 —— 那比继承更危险。「是否跨出了沙箱」只有真正拉起这个进程的人知道，不能靠
//     一个变量名前缀去推断。
//
// 判据一旦写下就得自己守住：我第一版把这三个也摘了，正好违反了上面那段注释。


func ptyEnv(env []string) []string {
	drop := make(map[string]struct{}, len(agentSessionMarkers))
	for _, k := range agentSessionMarkers {
		drop[k] = struct{}{}
	}
	out := make([]string, 0, len(env)+2)
	for _, item := range env {
		if strings.HasPrefix(item, "TERM=") || strings.HasPrefix(item, "COLORTERM=") {
			continue
		}
		// 按**键**精确比对，不是按前缀 —— 见 agentSessionMarkers 的注释。没有 '=' 的畸形条目
		// 原样放行（那不是我们要管的事）。
		if i := strings.IndexByte(item, '='); i > 0 {
			if _, bad := drop[item[:i]]; bad {
				continue
			}
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

	// OnSignal is called from the PTY read goroutine for every EXPLICIT out-of-band signal
	// the program emitted (BEL / OSC notification — see ansisignal). It is a pure tap: the
	// bytes are already on their way to the browser by the time it runs, and nothing it does
	// can alter them.
	//
	// Must be set BEFORE the first session is created (NewServer does): read loops run
	// concurrently, so assigning it later is a data race. It runs on the output hot path, so
	// the implementation must not block — see onSessionSignal for how that is honoured.
	OnSignal func(*Session, ansisignal.Signal)
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
// List returns every live session in STABLE CREATION ORDER (oldest first).
//
// The sort is load-bearing, not cosmetic. sync.Map.Range visits in an unspecified order that
// varies between calls, and pro's CLI derives its whole tab strip straight from this list — so
// an unsorted List() meant:
//   - a newly created terminal could appear anywhere in the strip, including first, where the
//     position-based label renders it as "终端1" while an older tab is also showing "终端1";
//   - the numbers reshuffled on every poll, which silently breaks the two things built on top of
//     them — `prefix+N` jump-to-tab and the overview card numbering (both promise "the number you
//     see is the number you press").
//
// Creation order is also simply what a user means by "the new tab goes on the end". Ties (two
// sessions created inside the same clock tick) fall back to ID so the order is total and never
// flickers between two equally-old sessions.
func (m *SessionManager) List() []*Session {
	var result []*Session
	m.sessions.Range(func(_, value any) bool {
		result = append(result, value.(*Session))
		return true
	})
	sort.Slice(result, func(i, j int) bool {
		if result[i].CreatedAt.Equal(result[j].CreatedAt) {
			return result[i].ID < result[j].ID
		}
		return result[i].CreatedAt.Before(result[j].CreatedAt)
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

	// Reap to avoid zombies. Goes through reap() rather than Cmd.Wait() directly because
	// readLoop is racing us to reap the very process we just killed.
	sess.reap()

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
	// One scanner per session: it carries the parser state that lets an OSC sequence split
	// across PTY read boundaries (which happens constantly) still be recognised.
	var signals ansisignal.Scanner
	defer func() {
		// Reap before touching ProcessState: reap() is the barrier that makes the read safe
		// even when Destroy is concurrently killing this same process (see Session.reap).
		sess.reap()
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

			// Signal tap — deliberately AFTER the buffer write and the subscriber fan-out, so
			// nothing here can delay a single byte reaching the user's terminal. It is a pure
			// observer: the scanner never consumes or rewrites the stream (see ansisignal).
			if m.OnSignal != nil {
				for _, sig := range signals.Feed(data) {
					m.OnSignal(sess, sig)
				}
			}
		}
		if err != nil {
			if err != io.EOF {
				logger.Debug("pty read error", "id", sess.ID, "error", err)
			}
			// No reap here: the deferred exit handler above reaps on every return path,
			// so keeping a second call site would only re-introduce two owners.
			return
		}
	}
}
