package terminal

import "time"

// Bracketed paste (DECSET 2004) is negotiated by the PROGRAM in the PTY — Codex, Claude Code,
// a modern shell each switch it on once, when they start. A paste that arrives unframed is, to
// such a program, a burst of individual keystrokes: Codex re-renders its whole composer per
// character (a long sentence "types itself" for tens of seconds), and every newline in it is an
// Enter. So the browser frames a paste only when it believes the mode is on, and what it
// believes is whatever the byte stream last told its terminal.
//
// That stream is bounded. After a server restart (or any reconnect) the browser gets a replay
// window that usually no longer contains the one DECSET that announced the mode, so this
// server tells it the mode explicitly (replayWithPasteMode). The defect this file closes: the
// server's own copy of the mode was a bool in memory, derived only from the bytes THIS process
// had seen. A restarted server therefore started at "off" — indistinguishable from "the
// program asked for off" — and forced that onto every browser on every reconnect, for as long
// as the program kept running. The mode was never off; the server just hadn't been there.
//
// Two rules fix it:
//  1. The mode has three values. "Unknown" is a first-class answer, and an unknown mode is never
//     asserted to a browser — absence of evidence must not be sent as evidence.
//  2. The last observed value lives in the daemon's meta blob (sessionMeta.Paste), which
//     outlives the server, so a restarted server starts from what the program last negotiated
//     instead of from nothing. The replay it then scans can only refine that, in stream order.
const (
	pasteModeUnknown int32 = iota
	pasteModeOn
	pasteModeOff
)

// pasteSaveDelay coalesces writes to the daemon. Shells toggle the mode around every command
// (off while it runs, on at the next prompt), and a restore scans a megabyte of history that
// may hold hundreds of toggles; the daemon only needs the value the burst settled on.
var pasteSaveDelay = 750 * time.Millisecond

// PasteMode reports whether the program in this session last negotiated bracketed paste, and
// whether that is known at all. Callers must not read known=false as enabled=false.
func (s *Session) PasteMode() (enabled, known bool) {
	switch s.paste.Load() {
	case pasteModeOn:
		return true, true
	case pasteModeOff:
		return false, true
	}
	return false, false
}

// notePasteMode records a DECSET/DECRST 2004 observed in the output stream — live or replayed
// scrollback; both are real observations, the last in stream order wins.
func (s *Session) notePasteMode(enabled bool) {
	next := pasteModeOff
	if enabled {
		next = pasteModeOn
	}
	if s.paste.Swap(next) != next {
		s.schedulePasteSave()
	}
}

// seedPasteMode adopts a value persisted by an earlier server. It only fills an unknown: any
// value observed by this process is newer than what the daemon remembers.
func (s *Session) seedPasteMode(persisted *bool) {
	if persisted == nil {
		return
	}
	v := pasteModeOff
	if *persisted {
		v = pasteModeOn
	}
	if s.paste.CompareAndSwap(pasteModeUnknown, v) {
		// The daemon already holds exactly this; writing it back would be a no-op round trip.
		s.pasteSaved.Store(v)
	}
}

func (s *Session) schedulePasteSave() {
	if !s.pasteSaving.CompareAndSwap(false, true) {
		return
	}
	time.AfterFunc(pasteSaveDelay, func() {
		// Cleared BEFORE the save: a toggle that lands while the write is in flight must be able
		// to schedule the next one rather than be absorbed into a write that already read the
		// older value.
		s.pasteSaving.Store(false)
		s.savePasteMode()
	})
}

// savePasteMode writes the current mode into the daemon's meta blob. Failure is logged, not
// returned: the mode is also re-derived from the stream, and the next change retries.
func (s *Session) savePasteMode() {
	s.mu.Lock()
	meta := s.metaLocked()
	client := s.mux
	save := s.metaSaver
	s.mu.Unlock()

	// What is written and what is remembered as written come from the SAME snapshot; reading
	// the atomic a second time could record a value the blob does not contain.
	state := pasteModeUnknown
	if meta.Paste != nil {
		state = pasteModeOff
		if *meta.Paste {
			state = pasteModeOn
		}
	}
	if state == pasteModeUnknown || state == s.pasteSaved.Load() {
		return
	}
	if save == nil {
		if client == nil {
			return
		}
		save = client.SetMeta
	}
	if err := save(s.ID, meta.encode()); err != nil {
		logger.Warn("could not persist the paste mode to the daemon; a server restart "+
			"before the next change will have to re-derive it from scrollback",
			"id", s.ID, "error", err)
		return
	}
	s.pasteSaved.Store(state)
}

// metaLocked is THE snapshot of what this server writes into the daemon's meta blob. Every
// writer goes through it because SetMeta replaces the whole blob: a writer that rebuilds the
// struct by hand erases whatever it forgot. Caller holds s.mu.
func (s *Session) metaLocked() sessionMeta {
	meta := sessionMeta{
		Name:      s.Name,
		Title:     s.Title,
		Engine:    s.Engine,
		CWD:       s.CWD,
		ShellPath: s.ShellPath,
		CreatedAt: s.CreatedAt,
		Origin:    s.Origin,
	}
	if enabled, known := s.PasteMode(); known {
		meta.Paste = &enabled
	}
	return meta
}
