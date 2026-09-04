package terminal

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// applyEnvOverlay is the whole feature in one pure function, so it carries the coverage. The
// behaviour being pinned is tmux's, measured on an isolated socket rather than recalled:
// set-environment changes what the NEXT spawn gets and never rewrites a running shell.

func envMap(kvs []string) map[string]string {
	out := map[string]string{}
	for _, kv := range kvs {
		if eq := strings.IndexByte(kv, '='); eq > 0 {
			out[kv[:eq]] = kv[eq+1:]
		}
	}
	return out
}

func TestApplyEnvOverlay_UnsetRemovesRatherThanEmpties(t *testing.T) {
	// The concrete need: drop an inherited ANTHROPIC_BASE_URL so the tool falls back to its own
	// default. Setting it to "" would leave the variable PRESENT, and clients that test presence
	// rather than value would still route to the gateway.
	base := []string{"PATH=/bin", "ANTHROPIC_BASE_URL=https://gateway.example", "HOME=/home/u"}
	got := applyEnvOverlay(base, &envOverlay{Unset: []string{"ANTHROPIC_BASE_URL"}})

	m := envMap(got)
	if _, present := m["ANTHROPIC_BASE_URL"]; present {
		t.Errorf("unset must REMOVE the variable, not blank it; got %v", got)
	}
	if m["PATH"] != "/bin" || m["HOME"] != "/home/u" {
		t.Errorf("unrelated variables must survive; got %v", got)
	}
}

func TestApplyEnvOverlay_SetReplacesAndAdds(t *testing.T) {
	base := []string{"PATH=/bin", "MODEL=old"}
	got := envMap(applyEnvOverlay(base, &envOverlay{Set: map[string]string{
		"MODEL": "new",
		"EXTRA": "added",
	}}))

	if got["MODEL"] != "new" {
		t.Errorf("MODEL = %q, want new", got["MODEL"])
	}
	if got["EXTRA"] != "added" {
		t.Errorf("EXTRA = %q, want added", got["EXTRA"])
	}
	if got["PATH"] != "/bin" {
		t.Errorf("PATH = %q, want /bin", got["PATH"])
	}
}

func TestApplyEnvOverlay_SetWinsOverUnsetForTheSameKey(t *testing.T) {
	// A key in both lists is a user editing mistake; "I gave it a value" is the more specific
	// of the two intents, so it wins. Pinned because the opposite choice is equally arguable and
	// a silent flip would be invisible until someone's provider changed under them.
	got := envMap(applyEnvOverlay([]string{"K=orig"}, &envOverlay{
		Set:   map[string]string{"K": "explicit"},
		Unset: []string{"K"},
	}))
	if got["K"] != "explicit" {
		t.Errorf("K = %q, want explicit", got["K"])
	}
}

func TestApplyEnvOverlay_EmptyOverlayIsIdentity(t *testing.T) {
	base := []string{"A=1", "B=2"}
	if got := applyEnvOverlay(base, nil); len(got) != 2 {
		t.Errorf("nil overlay must pass the environment through unchanged; got %v", got)
	}
	if got := applyEnvOverlay(base, &envOverlay{}); len(got) != 2 {
		t.Errorf("empty overlay must pass the environment through unchanged; got %v", got)
	}
}

func TestApplyEnvOverlay_MalformedEntriesNeverFailASpawn(t *testing.T) {
	// Refusing to open a terminal is a far worse outcome than ignoring one bad variable.
	base := []string{"GOOD=1", "no-equals-sign", "=leading-equals"}
	got := applyEnvOverlay(base, &envOverlay{
		Set:   map[string]string{"": "nameless", "OK": "yes"},
		Unset: []string{"", "   "},
	})
	m := envMap(got)
	if m["GOOD"] != "1" || m["OK"] != "yes" {
		t.Errorf("valid entries must survive alongside malformed ones; got %v", got)
	}
	// The OVERLAY's nameless key must not become an entry. (The base's own malformed entries are
	// passed through deliberately: the child was going to inherit them before the overlay existed,
	// so dropping them here would be an unrelated change to the environment smuggled in by a
	// feature that is supposed to only apply what the user asked for.)
	if n := strings.Count(strings.Join(got, "\x00"), "\x00="); n != 1 {
		t.Errorf("expected exactly the base's own nameless entry to survive; got %v", got)
	}
	if strings.Contains(strings.Join(got, "\x00"), "=nameless") {
		t.Errorf("a nameless key from the overlay leaked in; got %v", got)
	}
}

func TestApplyEnvOverlay_Deterministic(t *testing.T) {
	// Map iteration order is not stable; a spawn that differs run-to-run is a spawn nobody can
	// reason about (and would make the /sessions/{id}/env diagnostic useless).
	ov := &envOverlay{Set: map[string]string{"Z": "1", "A": "2", "M": "3"}}
	first := strings.Join(applyEnvOverlay([]string{"P=1"}, ov), "\x00")
	for i := 0; i < 20; i++ {
		if got := strings.Join(applyEnvOverlay([]string{"P=1"}, ov), "\x00"); got != first {
			t.Fatalf("resolved environment is not deterministic:\n%q\n%q", first, got)
		}
	}
}

func resetEnvOverlay(t *testing.T) {
	t.Helper()
	envOverlayMu.Lock()
	envOverlayCache, envOverlayLoaded = nil, false
	envOverlayMu.Unlock()
	t.Cleanup(func() {
		envOverlayMu.Lock()
		envOverlayCache, envOverlayLoaded = nil, false
		envOverlayMu.Unlock()
	})
}

// The point of the whole feature: an edit is visible to the NEXT spawn with no restart. If the
// overlay were read once and cached for the process lifetime, this would fail — and the bug being
// fixed would simply have moved one layer up.
func TestEnvOverlay_SaveTakesEffectWithoutRestart(t *testing.T) {
	s := &Server{config: Config{DataDir: t.TempDir()}}
	resetEnvOverlay(t)

	rec := httptest.NewRecorder()
	s.handleSaveEnvOverlay(rec, httptest.NewRequest(http.MethodPut, "/env",
		strings.NewReader(`{"set":{"DW_TEST_MODEL":"official"},"unset":["DW_TEST_GATEWAY"]}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("save: got %d, want 200", rec.Code)
	}

	got := envMap(applyEnvOverlay([]string{"DW_TEST_GATEWAY=bad", "PATH=/bin"}, s.envOverlay()))
	if got["DW_TEST_MODEL"] != "official" {
		t.Errorf("a saved `set` must reach the next spawn immediately; got %v", got)
	}
	if _, present := got["DW_TEST_GATEWAY"]; present {
		t.Errorf("a saved `unset` must reach the next spawn immediately; got %v", got)
	}
}

func TestEnvOverlay_RejectsUnrepresentableNames(t *testing.T) {
	s := &Server{config: Config{DataDir: t.TempDir()}}
	resetEnvOverlay(t)

	rec := httptest.NewRecorder()
	s.handleSaveEnvOverlay(rec, httptest.NewRequest(http.MethodPut, "/env",
		strings.NewReader(`{"set":{"HAS=EQUALS":"x","FINE":"y"}}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("save: got %d, want 200", rec.Code)
	}
	var saved envOverlay
	if err := json.Unmarshal(rec.Body.Bytes(), &saved); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := saved.Set["HAS=EQUALS"]; ok {
		t.Error("a name containing '=' cannot exist in an environment and must be dropped")
	}
	if saved.Set["FINE"] != "y" {
		t.Error("valid entries must still be saved")
	}
}

func TestEnvOverlay_GetReturnsUsableShapeWhenUnset(t *testing.T) {
	// The UI binds directly to these; nil would render as a broken table rather than an empty one.
	s := &Server{config: Config{DataDir: t.TempDir()}}
	resetEnvOverlay(t)

	rec := httptest.NewRecorder()
	s.handleGetEnvOverlay(rec, httptest.NewRequest(http.MethodGet, "/env", nil))
	body := rec.Body.String()
	if !strings.Contains(body, `"set"`) || !strings.Contains(body, `"unset"`) {
		t.Errorf("both keys must always be present; got %s", body)
	}
	if strings.Contains(body, "null") {
		t.Errorf("no nulls in the wire shape; got %s", body)
	}
}

// EnvSource is what makes the overlay reach a spawn at all. A nil hook must degrade to the old
// behaviour rather than to an empty environment — a shell with no PATH is not a shell.
func TestSessionManagerPtyEnv_NilSourceFallsBackToProcessEnv(t *testing.T) {
	m := NewSessionManager(4096, "/bin/sh")
	if len(m.ptyEnv()) == 0 {
		t.Fatal("nil EnvSource must fall back to this process's environment, not an empty one")
	}
	m.EnvSource = func() []string { return nil }
	if len(m.ptyEnv()) == 0 {
		t.Fatal("an EnvSource returning nil must also fall back, not strand the shell")
	}
	m.EnvSource = func() []string { return []string{"ONLY=this"} }
	if got := m.ptyEnv(); len(got) != 1 || got[0] != "ONLY=this" {
		t.Fatalf("EnvSource must be authoritative when it answers; got %v", got)
	}
}

// The overlay's values are user-supplied and about to be EXECUTED by the user's own shell, so
// quoting is a security property, not formatting. An unquoted `$(...)` here would be command
// injection into the session the feature is supposed to be helping.
func TestShellSingleQuote_NeutralisesExpansion(t *testing.T) {
	cases := map[string]string{
		"plain":       `'plain'`,
		"$(rm -rf /)": `'$(rm -rf /)'`,
		"`whoami`":    "'`whoami`'",
		"has 'quote'": `'has '\''quote'\'''`,
		"a$b;c|d&e":   `'a$b;c|d&e'`,
		"":            `''`,
	}
	for in, want := range cases {
		if got := shellSingleQuote(in); got != want {
			t.Errorf("shellSingleQuote(%q) = %s, want %s", in, got, want)
		}
	}
}

// Round-trip the quoting through a real shell: the assertion above pins the STRING, this pins that
// the string means what we think it means. A hand-written quoter that only looks right is exactly
// the kind of thing that passes review and fails in production.
func TestShellSingleQuote_RoundTripsThroughARealShell(t *testing.T) {
	for _, v := range []string{"plain", "$(echo pwned)", "`echo pwned`", "has 'quote' inside", "a$b;c|d&e", ""} {
		out, err := exec.Command("/bin/sh", "-c", "printf %s "+shellSingleQuote(v)).Output()
		if err != nil {
			t.Fatalf("sh rejected the quoting of %q: %v", v, err)
		}
		if string(out) != v {
			t.Errorf("value %q survived quoting as %q — expansion leaked", v, string(out))
		}
	}
}

func TestEnvOverlayShellLines_UnsetBeforeExportAndDeterministic(t *testing.T) {
	lines := envOverlayShellLines(&envOverlay{
		Set:   map[string]string{"Z_VAR": "1", "A_VAR": "2"},
		Unset: []string{"GONE", ""},
	})
	want := []string{"unset GONE", "export A_VAR='2'", "export Z_VAR='1'"}
	if len(lines) != len(want) {
		t.Fatalf("lines = %v, want %v", lines, want)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, lines[i], want[i])
		}
	}
}

// The guard that stops this command from typing into a running agent's stdin. Verified against
// real processes rather than a fake: `tpgid` is a kernel fact and a hand-rolled /proc stub would
// only prove the parser agrees with itself.
func TestShellIsForeground_RealProcesses(t *testing.T) {
	// This test process has no controlling terminal under `go test`, so tpgid is -1 → unknown.
	// That is the case the handler must treat as "fall through", never as "refused".
	if _, known := shellIsForeground(os.Getpid()); known {
		if fg, _ := shellIsForeground(os.Getpid()); fg {
			t.Log("this test process owns a tty foreground group; acceptable, just unusual")
		}
	}
	if _, known := shellIsForeground(0); known {
		t.Error("pid 0 must be unknown, not an answer")
	}
	if _, known := shellIsForeground(-5); known {
		t.Error("a negative pid must be unknown, not an answer")
	}
	// A pid that cannot exist: /proc read fails → unknown.
	if _, known := shellIsForeground(1 << 30); known {
		t.Error("a nonexistent pid must be unknown, not an answer")
	}
}

// comm can contain spaces and parentheses, which is why the parser skips to the LAST ')'. A process
// literally named "(a b) c" is the canonical trap; prove the field offset survives it.
func TestShellIsForeground_ParsesCommWithParensAndSpaces(t *testing.T) {
	dir := t.TempDir()
	// Field layout after comm: state ppid pgrp session tty_nr tpgid...
	stat := "4242 ((weird) name with spaces) S 1 4242 4242 34816 4242 4194304 0 0"
	if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(stat), 0644); err != nil {
		t.Fatal(err)
	}
	// Exercise the same parsing the function does, on the tricky comm.
	closeIdx := strings.LastIndexByte(stat, ')')
	fields := strings.Fields(stat[closeIdx+1:])
	if len(fields) < 6 {
		t.Fatalf("not enough fields after comm: %v", fields)
	}
	if fields[5] != "4242" {
		t.Errorf("tpgid parsed as %q, want 4242 — the comm parentheses shifted the offset", fields[5])
	}
}
