package terminal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// resetUploadLimitState restores package-level upload-limit state (upload_limit.go) to
// the fresh-process default before AND after a test. These tests mutate global state
// (uploadLimitBytes, uploadLimitDir), so without this a test's Set() would leak into
// whichever test happens to run next in the same binary.
func resetUploadLimitState(t *testing.T) {
	t.Helper()
	reset := func() {
		uploadLimitBytes.Store(ClipboardMaxUploadSize)
		uploadLimitDirMu.Lock()
		uploadLimitDir = ""
		uploadLimitDirMu.Unlock()
	}
	reset()
	t.Cleanup(reset)
}

// TestUploadLimitDefault verifies the zero-touch default: before any Load/Set call,
// UploadLimitBytes reports the compile-time ClipboardMaxUploadSize (10 MB) — the value
// a caller that never adopts this runtime SSOT (e.g. pro, until it switches) still
// sees.
func TestUploadLimitDefault(t *testing.T) {
	resetUploadLimitState(t)
	if got, want := UploadLimitBytes(), int64(ClipboardMaxUploadSize); got != want {
		t.Fatalf("UploadLimitBytes() = %d, want default %d", got, want)
	}
}

// TestUploadLimitSetWithinRange verifies an in-range Set applies exactly and is
// immediately visible via UploadLimitBytes, and persists to <dataDir>/upload-limit.json.
func TestUploadLimitSetWithinRange(t *testing.T) {
	resetUploadLimitState(t)
	dir := t.TempDir()
	if err := LoadUploadLimit(dir); err != nil {
		t.Fatalf("LoadUploadLimit: %v", err)
	}
	applied, err := SetUploadLimitMB(100)
	if err != nil {
		t.Fatalf("SetUploadLimitMB(100): %v", err)
	}
	if applied != 100 {
		t.Fatalf("applied = %d, want 100", applied)
	}
	if got, want := UploadLimitBytes(), int64(100)<<20; got != want {
		t.Fatalf("UploadLimitBytes() = %d, want %d", got, want)
	}
	if _, err := os.Stat(filepath.Join(dir, uploadLimitFileName)); err != nil {
		t.Fatalf("upload-limit.json was not written: %v", err)
	}
}

// TestUploadLimitClampBoundaries verifies the [1, 1024] MB hard floor/ceiling: values
// under 1 clamp up to 1, values over 1024 clamp down to 1024, and the exact boundary
// values (1, 1024) pass through unchanged.
func TestUploadLimitClampBoundaries(t *testing.T) {
	resetUploadLimitState(t)
	dir := t.TempDir()
	if err := LoadUploadLimit(dir); err != nil {
		t.Fatalf("LoadUploadLimit: %v", err)
	}

	cases := []struct {
		name string
		in   int
		want int
	}{
		{"zero clamps to floor", 0, 1},
		{"negative clamps to floor", -5, 1},
		{"exact floor passes through", 1, 1},
		{"exact ceiling passes through", 1024, 1024},
		{"just above ceiling clamps down", 1025, 1024},
		{"way above ceiling clamps down", 999999, 1024},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			applied, err := SetUploadLimitMB(tc.in)
			if err != nil {
				t.Fatalf("SetUploadLimitMB(%d): %v", tc.in, err)
			}
			if applied != tc.want {
				t.Fatalf("SetUploadLimitMB(%d) = %d, want %d", tc.in, applied, tc.want)
			}
			if got, want := UploadLimitBytes(), int64(tc.want)<<20; got != want {
				t.Fatalf("UploadLimitBytes() after Set(%d) = %d, want %d", tc.in, got, want)
			}
		})
	}
}

// TestUploadLimitLoadReadsPersistedValue verifies the Set→persist→(simulated
// restart)→Load round trip: a value set against a DataDir is read back correctly by a
// fresh LoadUploadLimit call against that SAME dir, as happens across a real server
// restart. It also checks the on-disk JSON shape directly ({"maxMb": N}).
func TestUploadLimitLoadReadsPersistedValue(t *testing.T) {
	resetUploadLimitState(t)
	dir := t.TempDir()
	if err := LoadUploadLimit(dir); err != nil {
		t.Fatalf("LoadUploadLimit (initial): %v", err)
	}
	if _, err := SetUploadLimitMB(250); err != nil {
		t.Fatalf("SetUploadLimitMB(250): %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, uploadLimitFileName))
	if err != nil {
		t.Fatalf("read %s: %v", uploadLimitFileName, err)
	}
	var onDisk struct {
		MaxMB int `json:"maxMb"`
	}
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatalf("unmarshal %s: %v", uploadLimitFileName, err)
	}
	if onDisk.MaxMB != 250 {
		t.Fatalf("on-disk maxMb = %d, want 250", onDisk.MaxMB)
	}

	// Simulate a process restart: reset the in-memory value to the compile-time
	// default, then Load from the same DataDir — it must recover 250, not the default.
	uploadLimitBytes.Store(ClipboardMaxUploadSize)
	if err := LoadUploadLimit(dir); err != nil {
		t.Fatalf("LoadUploadLimit (reload after simulated restart): %v", err)
	}
	if got, want := UploadLimitBytes(), int64(250)<<20; got != want {
		t.Fatalf("UploadLimitBytes() after reload = %d, want %d", got, want)
	}
}

// TestUploadLimitLoadMissingFileUsesDefault verifies a DataDir with no
// upload-limit.json (the common case: never overridden) loads cleanly with no error
// and leaves the compile-time ClipboardMaxUploadSize default in effect.
func TestUploadLimitLoadMissingFileUsesDefault(t *testing.T) {
	resetUploadLimitState(t)
	dir := t.TempDir() // empty — no upload-limit.json in it
	if err := LoadUploadLimit(dir); err != nil {
		t.Fatalf("LoadUploadLimit on a dir with no persisted file should not error, got: %v", err)
	}
	if got, want := UploadLimitBytes(), int64(ClipboardMaxUploadSize); got != want {
		t.Fatalf("UploadLimitBytes() = %d, want default %d", got, want)
	}
}

// TestUploadLimitSetWithoutLoadAppliesInMemory verifies SetUploadLimitMB applied
// before any LoadUploadLimit call (no DataDir known yet) still updates the in-memory
// effective limit — persistence is best-effort, never a precondition for the new
// limit taking effect immediately.
func TestUploadLimitSetWithoutLoadAppliesInMemory(t *testing.T) {
	resetUploadLimitState(t)
	applied, err := SetUploadLimitMB(42)
	if err != nil {
		t.Fatalf("SetUploadLimitMB without a prior Load: %v", err)
	}
	if applied != 42 {
		t.Fatalf("applied = %d, want 42", applied)
	}
	if got, want := UploadLimitBytes(), int64(42)<<20; got != want {
		t.Fatalf("UploadLimitBytes() = %d, want %d", got, want)
	}
}
