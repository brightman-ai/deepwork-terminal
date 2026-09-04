package terminal

// Terminal environment overlay — "what environment does a NEW shell start with".
//
// # The hole this closes
//
// A new PTY's environment used to be exactly `os.Environ()`: whatever the shell that launched
// THIS server process happened to be carrying. That is a snapshot taken at process start, and a
// Go process cannot change its own inherited environment retroactively — so once the server was
// up, "what every new terminal gets" was frozen until someone restarted it.
//
// It bit for real on 2026-08-25: the shell that started :18074 had `ANTHROPIC_BASE_URL` and
// friends pointing at a third-party model gateway, left over from an unrelated experiment. Every
// terminal opened from that moment on inherited them, so `claude` in a new tab silently talked to
// the wrong provider — and no amount of exporting in any OTHER shell could fix it, because none of
// them was the server's parent. The only lever was a full restart, whose side effects are global
// and whose whole point (with the resident daemon) was supposed to be that you never need one.
//
// # Why it looks like tmux
//
// tmux solved this shape long ago and the mechanism is worth copying exactly: the tmux server does
// NOT hand its own `environ` to new panes. It keeps a separate, mutable environment table
// (`set-environment`), and every spawn resolves against that table instead. Changing it takes
// effect on the next pane immediately, with no server restart.
//
// Measured, not remembered (isolated socket, 2026-09-04):
//
//	pane: echo $FOO            → empty
//	tmux set-environment FOO bar
//	tmux show-environment      → FOO=bar
//	SAME pane: echo $FOO       → still empty      ← a running shell is never rewritten
//	new-window: echo $FOO      → bar              ← only new spawns see it
//
// So "affects the next one, never the current one" is not a limitation of this design; it is the
// only thing any design can do. A process's environment lives in that process's memory and nothing
// outside it can reach in. The honest way to change a RUNNING shell is to type into it, which is
// what the visible `export`/`unset` injection is for — and that is strictly more than tmux offers.
//
// # Resolution order
//
//	os.Environ()  →  minus `unset`  →  plus `set`
//
// `unset` is applied first, so a key named in both ends up SET. A key in both is a user editing
// mistake either way, and "I gave it a value" is the more specific of the two intents.
//
// `unset` earns its place rather than being redundant with "set it to empty": the concrete need is
// *removing* an inherited `ANTHROPIC_BASE_URL` so the tool falls back to its own default. Setting
// it to "" would leave the variable present, and a client that checks presence rather than value
// (most do) would behave as if the gateway were still configured. This is tmux's `-r`/`-u`.

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// envOverlay is the user-owned delta between "the environment this server happens to have" and
// "the environment a new terminal should get".
type envOverlay struct {
	// Set adds or replaces variables.
	Set map[string]string `json:"set"`
	// Unset REMOVES variables that would otherwise be inherited. Not the same as setting them
	// empty — see the file header.
	Unset []string `json:"unset"`
}

var (
	envOverlayMu     sync.RWMutex
	envOverlayCache  *envOverlay
	envOverlayLoaded bool
)

// applyEnvOverlay resolves base (KEY=VALUE strings, as from os.Environ) against the overlay.
//
// Pure and total on purpose: it is the one piece of this feature worth exhaustive unit coverage,
// and it must never fail a spawn — a malformed entry is skipped, not fatal, because refusing to
// open a terminal is a far worse outcome than ignoring one bad variable.
func applyEnvOverlay(base []string, ov *envOverlay) []string {
	if ov == nil || (len(ov.Set) == 0 && len(ov.Unset) == 0) {
		return base
	}

	// Keep insertion order of the inherited environment: some tools (and humans reading
	// `/proc/<pid>/environ` while debugging) find a stable order easier to trust, and there is
	// no reason to shuffle it.
	drop := make(map[string]bool, len(ov.Unset))
	for _, k := range ov.Unset {
		if k = strings.TrimSpace(k); k != "" {
			drop[k] = true
		}
	}

	out := make([]string, 0, len(base)+len(ov.Set))
	seen := make(map[string]bool, len(base))
	for _, kv := range base {
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 {
			out = append(out, kv) // not a KEY=VALUE pair; pass it through untouched
			continue
		}
		key := kv[:eq]
		// Set is consulted BEFORE Unset, which is what makes a key named in both end up set.
		// The order is the whole implementation of that rule — checking drop first (the obvious
		// way to write this) silently inverts it, and the inversion is invisible until someone's
		// provider changes under them. Pinned by TestApplyEnvOverlay_SetWinsOverUnsetForTheSameKey.
		if v, ok := ov.Set[key]; ok {
			out = append(out, key+"="+v)
			seen[key] = true
			continue
		}
		if drop[key] {
			continue
		}
		out = append(out, kv)
		seen[key] = true
	}

	// Additions the base did not have. Sorted so the resolved environment is deterministic —
	// map iteration order is not, and a spawn that differs run-to-run is a spawn nobody can
	// reason about.
	extra := make([]string, 0, len(ov.Set))
	for k := range ov.Set {
		// No `drop[k]` test here either, for the same reason as above: Set wins over Unset, so a
		// key in both must still be added when the base did not carry it.
		if k = strings.TrimSpace(k); k == "" || seen[k] {
			continue
		}
		extra = append(extra, k)
	}
	sort.Strings(extra)
	for _, k := range extra {
		out = append(out, k+"="+ov.Set[k])
	}
	return out
}

// ptyEnviron is what a NEW session's shell should start with. Wired into SessionManager as its
// EnvSource so the manager keeps knowing nothing about where the overlay is stored.
func (s *Server) ptyEnviron() []string {
	return applyEnvOverlay(os.Environ(), s.envOverlay())
}

func (s *Server) envOverlay() *envOverlay {
	envOverlayMu.RLock()
	if envOverlayLoaded {
		ov := envOverlayCache
		envOverlayMu.RUnlock()
		return ov
	}
	envOverlayMu.RUnlock()

	envOverlayMu.Lock()
	defer envOverlayMu.Unlock()
	if !envOverlayLoaded { // re-check: another goroutine may have loaded it while we waited
		envOverlayCache = s.loadEnvOverlay()
		envOverlayLoaded = true
	}
	return envOverlayCache
}

func (s *Server) envOverlayPath() string {
	return filepath.Join(s.dataDir(), "env.json")
}

func (s *Server) loadEnvOverlay() *envOverlay {
	data, err := os.ReadFile(s.envOverlayPath())
	if err != nil {
		return nil
	}
	var ov envOverlay
	if err := json.Unmarshal(data, &ov); err != nil {
		// A corrupt overlay must not silently become "no overlay" without a trace: the user
		// would open a terminal, get the wrong provider, and have nothing to look at.
		logger.Warn("terminal env overlay is unreadable; new shells will use the inherited "+
			"environment unchanged", "path", s.envOverlayPath(), "error", err)
		return nil
	}
	return &ov
}

func (s *Server) handleGetEnvOverlay(w http.ResponseWriter, r *http.Request) {
	ov := s.envOverlay()
	if ov == nil {
		ov = &envOverlay{}
	}
	if ov.Set == nil {
		ov.Set = map[string]string{}
	}
	if ov.Unset == nil {
		ov.Unset = []string{}
	}
	// Explicit status, like the store/workbench handlers: under the pro embed this is reached
	// through gin's NoRoute forward, which pre-sets 404 before the handler runs.
	writeJSON(w, http.StatusOK, ov)
}

func (s *Server) handleSaveEnvOverlay(w http.ResponseWriter, r *http.Request) {
	var ov envOverlay
	if err := json.NewDecoder(r.Body).Decode(&ov); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	// Names, not values, are validated: a variable name containing '=' or NUL cannot be
	// expressed in an environment at all, and letting one through would corrupt every later
	// entry rather than fail loudly here.
	clean := envOverlay{Set: map[string]string{}}
	for k, v := range ov.Set {
		if k = strings.TrimSpace(k); validEnvName(k) {
			clean.Set[k] = v
		}
	}
	for _, k := range ov.Unset {
		if k = strings.TrimSpace(k); validEnvName(k) {
			clean.Unset = append(clean.Unset, k)
		}
	}
	if clean.Unset == nil {
		clean.Unset = []string{}
	}

	data, err := json.MarshalIndent(clean, "", "  ")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "encode failed"})
		return
	}
	path := s.envOverlayPath()
	os.MkdirAll(filepath.Dir(path), 0755) //nolint:errcheck
	// 0600, not 0644: this file is where API tokens for model providers end up, and the
	// neighbouring store.json's 0644 is not a precedent worth following for credentials.
	if err := os.WriteFile(path, data, 0600); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "write failed"})
		return
	}

	envOverlayMu.Lock()
	envOverlayCache = &clean
	envOverlayLoaded = true
	envOverlayMu.Unlock()

	// No restart needed, and that is the entire point of the feature: the NEXT session created
	// resolves against this, exactly like `tmux set-environment` followed by `new-window`.
	// Sessions already running are untouched — see the file header for why that is not a
	// shortcoming but the only possible behaviour.
	writeJSON(w, http.StatusOK, clean)
}

// handleApplyEnvHere types the overlay into a RUNNING shell as `unset` / `export` lines.
//
// # Why typing is the only honest mechanism
//
// The overlay is resolved at spawn, so it reaches new terminals only — exactly like tmux's
// `set-environment` (measured; see the file header). A running shell's environment lives in that
// shell's own memory and nothing outside the process can reach in. The one thing that CAN change it
// is the shell itself, executing a command. So this writes the commands into the PTY, and the user
// watches them land: same bytes a keystroke takes, same echo, same history.
//
// That visibility is the point, not a side effect. Running commands in someone's shell without
// showing them is the black-box behaviour this project rules out; an `export` the user can read on
// screen is a request they can see, verify and undo.
//
// # Why it refuses when a program is in the foreground
//
// If the shell has launched something interactive — `claude`, an editor, a pager — these bytes do
// not reach the shell at all. They reach that program's stdin, which means this command would
// silently TYPE `unset ANTHROPIC_BASE_URL` into the user's agent conversation. That is worse than
// doing nothing, so it is refused with a reason instead.
//
// The foreground check reads `tpgid` from /proc — the same kernel fact TIOCGPGRP returns, and the
// same one that decides where Ctrl+C goes. Via /proc rather than an ioctl because the PTY belongs
// to the daemon, and adding a protocol round-trip for a question the filesystem already answers
// would buy nothing.
func (s *Server) handleApplyEnvHere(w http.ResponseWriter, r *http.Request) {
	sess, err := s.mgr.Get(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	lines := envOverlayShellLines(s.envOverlay())
	if len(lines) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"applied": []string{},
			"note": "还没有配置任何环境变量覆盖，没有可应用的东西"})
		return
	}

	pid := sess.ShellPID()
	fg, known := shellIsForeground(pid)
	if known && !fg {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "这个终端里有程序正在运行，命令会被它接收而不是被 shell 执行。" +
				"先退出那个程序（或新建一个终端）再试。",
		})
		return
	}
	// `known == false` (no /proc, or the process just went away) falls THROUGH rather than
	// refusing: on a host without /proc this command would otherwise never work at all, and the
	// bytes are visible either way — the user can see where they landed and undo it.

	payload := strings.Join(lines, "\n") + "\n"
	if err := sess.WriteInput([]byte(payload)); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"applied": lines})
}

// envOverlayShellLines renders the overlay as shell commands, unset first so the ordering matches
// applyEnvOverlay's resolution (a key in both ends up set).
func envOverlayShellLines(ov *envOverlay) []string {
	if ov == nil {
		return nil
	}
	var lines []string
	for _, k := range ov.Unset {
		if k = strings.TrimSpace(k); validEnvName(k) {
			lines = append(lines, "unset "+k)
		}
	}
	keys := make([]string, 0, len(ov.Set))
	for k := range ov.Set {
		if k = strings.TrimSpace(k); validEnvName(k) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys) // deterministic: the user is about to READ these lines
	for _, k := range keys {
		lines = append(lines, "export "+k+"="+shellSingleQuote(ov.Set[k]))
	}
	return lines
}

// shellSingleQuote makes a value safe to paste into a shell command line.
//
// This is not cosmetic. The value is user-supplied and about to be EXECUTED by their shell, so an
// unquoted `$(...)` or backtick would be command injection into the user's own session. Single
// quotes suppress every form of expansion; the only character needing care inside them is the
// single quote itself, closed and re-opened around an escaped one in the classic way.
func shellSingleQuote(v string) string {
	return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
}

// shellIsForeground reports whether the process itself — rather than something it launched — owns
// the terminal's foreground process group. The second return is false when the answer cannot be
// determined (no /proc, unparseable, process gone), which callers must treat as "unknown", never
// as "no".
func shellIsForeground(pid int) (fg bool, known bool) {
	if pid <= 0 {
		return false, false
	}
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false, false
	}
	// Field 2 is the executable name in parentheses and may itself contain spaces AND
	// parentheses, so everything before the LAST ')' is skipped rather than split on.
	close := strings.LastIndexByte(string(raw), ')')
	if close < 0 {
		return false, false
	}
	// After comm: state(3) ppid(4) pgrp(5) session(6) tty_nr(7) tpgid(8) → index 5 here.
	fields := strings.Fields(string(raw)[close+1:])
	if len(fields) < 6 {
		return false, false
	}
	tpgid, err := strconv.Atoi(fields[5])
	if err != nil {
		return false, false
	}
	// -1 means the process has no controlling terminal — not a PTY session we can reason about.
	if tpgid <= 0 {
		return false, false
	}
	return tpgid == pid, true
}

// validEnvName rejects names that cannot exist in a real environment. Deliberately permissive
// about everything else (lowercase, dots, unicode): plenty of tools use unconventional names, and
// this is a place to prevent corruption, not to enforce taste.
func validEnvName(k string) bool {
	if k == "" {
		return false
	}
	return !strings.ContainsAny(k, "=\x00")
}

// handleSessionEnv reports the environment a session's shell is ACTUALLY running with, read from
// the process itself.
//
// Read, not reconstructed. The alternative — remember what we passed at spawn — would drift the
// moment anything else was true: the overlay changed since, the shell's rc exported something, the
// user typed `export`. Since the honest way to change a running shell IS to type into it, a
// reconstruction would be wrong precisely when someone is trying to check whether their change
// took. /proc is the only answer that cannot lie.
func (s *Server) handleSessionEnv(w http.ResponseWriter, r *http.Request) {
	sess, err := s.mgr.Get(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	pid := sess.ShellPID()
	if pid <= 0 {
		writeJSON(w, http.StatusOK, map[string]any{"available": false, "vars": map[string]string{}})
		return
	}
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/environ")
	if err != nil {
		// Not an error worth a 5xx: the process may have just exited, and on a non-Linux host
		// /proc does not exist at all. Say so rather than pretending the environment is empty.
		writeJSON(w, http.StatusOK, map[string]any{"available": false, "vars": map[string]string{}})
		return
	}
	vars := map[string]string{}
	for _, kv := range strings.Split(string(raw), "\x00") {
		if eq := strings.IndexByte(kv, '='); eq > 0 {
			vars[kv[:eq]] = kv[eq+1:]
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"available": true, "vars": vars})
}
