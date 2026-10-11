package terminal

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

// recordingSaver stands in for the daemon's SetMeta.
type recordingSaver struct {
	mu    sync.Mutex
	blobs [][]byte
}

func (r *recordingSaver) save(_ string, blob []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.blobs = append(r.blobs, append([]byte(nil), blob...))
	return nil
}

func (r *recordingSaver) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.blobs)
}

func (r *recordingSaver) last() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.blobs) == 0 {
		return nil
	}
	return r.blobs[len(r.blobs)-1]
}

func newPasteTestSession(saver *recordingSaver) *Session {
	s := &Session{ID: "s1", Name: "work", Engine: "shell", CWD: "/w", Origin: "standalone"}
	if saver != nil {
		s.metaSaver = saver.save
	}
	return s
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func withFastPasteSave(t *testing.T) {
	t.Helper()
	old := pasteSaveDelay
	pasteSaveDelay = 10 * time.Millisecond
	t.Cleanup(func() { pasteSaveDelay = old })
}

// THE regression: the program announced bracketed paste once, at start. The server restarts, and
// the replay it scans no longer contains that DECSET. The restarted server must still tell the
// browser the mode is on — otherwise Codex receives every pasted character as a keystroke.
func TestRestartedServerKeepsPasteModeThatLeftTheReplayWindow(t *testing.T) {
	withFastPasteSave(t)
	saver := &recordingSaver{}

	// Server #1 watches the program start and negotiate the mode.
	before := newPasteTestSession(saver)
	scan := clipboardScanner{pasteMode: before.notePasteMode}
	scan.feed([]byte("\x1b[?1049h\x1b[?2004h> ready"), false, func(string, int64, bool) {})
	waitFor(t, "the mode to be persisted", func() bool { return saver.count() == 1 })

	// Server #2: a new process. All it has is the daemon's blob and a replay window that holds
	// only screen repaints — the DECSET scrolled out of the ring long ago.
	var meta sessionMeta
	if err := json.Unmarshal(saver.last(), &meta); err != nil {
		t.Fatal(err)
	}
	after := newPasteTestSession(nil)
	after.seedPasteMode(meta.Paste)
	window := []byte("\x1b[2J\x1b[Hrepaint only, no mode toggle in sight")
	scanAfter := clipboardScanner{pasteMode: after.notePasteMode}
	scanAfter.feed(window, true, func(string, int64, bool) {})

	enabled, known := after.PasteMode()
	if !known || !enabled {
		t.Fatalf("restarted server lost the negotiated mode: enabled=%t known=%t", enabled, known)
	}
	browser := false
	b := clipboardScanner{pasteMode: func(v bool) { browser = v }}
	b.feed(replayWithPasteMode(window, enabled, known), false, func(string, int64, bool) {})
	if !browser {
		t.Fatal("browser terminal ended up with bracketed paste off after the restart")
	}
}

func TestPasteModeStartsUnknown(t *testing.T) {
	s := newPasteTestSession(nil)
	if _, known := s.PasteMode(); known {
		t.Fatal("a session nobody has watched must not claim to know its paste mode")
	}
	if strings.Contains(string(replayWithPasteMode([]byte("x"), false, false)), "2004") {
		t.Fatal("an unknown mode was asserted to the browser")
	}
}

func TestPersistedPasteModeOnlyFillsAnUnknown(t *testing.T) {
	on, off := true, false

	s := newPasteTestSession(nil)
	s.seedPasteMode(&on)
	s.notePasteMode(false) // the program switched it off after the server came up
	if enabled, known := s.PasteMode(); !known || enabled {
		t.Fatalf("observation must beat the persisted seed, got enabled=%t known=%t", enabled, known)
	}

	s.seedPasteMode(&on) // a later reconcile re-reads the daemon's older copy
	if enabled, _ := s.PasteMode(); enabled {
		t.Fatal("a persisted value overwrote a newer observation")
	}

	fresh := newPasteTestSession(nil)
	fresh.seedPasteMode(nil)
	if _, known := fresh.PasteMode(); known {
		t.Fatal("a blob without the field must leave the mode unknown")
	}
	fresh.seedPasteMode(&off)
	if enabled, known := fresh.PasteMode(); !known || enabled {
		t.Fatalf("persisted off must be adopted as off, got enabled=%t known=%t", enabled, known)
	}
}

// A shell toggles the mode around every command. Only the settled value reaches the daemon, and
// a value the daemon already holds is never written again.
func TestPasteModeSavesAreCoalescedAndSkipNoOps(t *testing.T) {
	withFastPasteSave(t)
	saver := &recordingSaver{}
	s := newPasteTestSession(saver)

	for i := 0; i < 50; i++ {
		s.notePasteMode(i%2 == 1) // ends on true
	}
	waitFor(t, "one coalesced save", func() bool { return saver.count() >= 1 })
	time.Sleep(60 * time.Millisecond)
	if n := saver.count(); n != 1 {
		t.Fatalf("expected one coalesced write, got %d", n)
	}
	var meta sessionMeta
	if err := json.Unmarshal(saver.last(), &meta); err != nil {
		t.Fatal(err)
	}
	if meta.Paste == nil || !*meta.Paste {
		t.Fatalf("settled value not persisted: %+v", meta.Paste)
	}

	// off then on again inside one debounce window: the daemon already says on.
	s.notePasteMode(false)
	s.notePasteMode(true)
	time.Sleep(60 * time.Millisecond)
	if n := saver.count(); n != 1 {
		t.Fatalf("a value the daemon already holds was written again (%d writes)", n)
	}
}

// SetMeta replaces the whole blob. Every field the server owns has to survive a rewrite, or a
// rename would erase what the program negotiated (and the next restart would start from blank).
func TestMetaSnapshotCarriesEveryFieldAndOmitsUnknownPaste(t *testing.T) {
	s := newPasteTestSession(nil)
	s.CreatedAt = time.Unix(1700000000, 0).UTC()
	s.ShellPath = "/bin/zsh"
	s.Title = "T"

	s.mu.Lock()
	unknown := s.metaLocked()
	s.mu.Unlock()
	if unknown.Paste != nil {
		t.Fatal("unknown paste mode must not be written as a value")
	}
	if strings.Contains(string(unknown.encode()), `"paste"`) {
		t.Fatalf("unknown mode leaked into the blob: %s", unknown.encode())
	}

	s.notePasteMode(false)
	s.mu.Lock()
	known := s.metaLocked()
	s.mu.Unlock()
	if known.Paste == nil || *known.Paste {
		t.Fatalf("an observed OFF must be persisted as false, got %v", known.Paste)
	}
	if !strings.Contains(string(known.encode()), `"paste":false`) {
		t.Fatalf("false was dropped by omitempty: %s", known.encode())
	}
	if known.Name != "work" || known.Title != "T" || known.Engine != "shell" || known.CWD != "/w" ||
		known.ShellPath != "/bin/zsh" || known.Origin != "standalone" || !known.CreatedAt.Equal(s.CreatedAt) {
		t.Fatalf("snapshot dropped a field: %+v", known)
	}
}
