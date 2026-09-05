// Package muxd implements dw-muxd: the resident daemon that owns every PTY, and the
// client half that dw-terminal uses to reach it.
//
// Why this package exists: before it, a PTY was a direct child of the dw-terminal
// process (pty.Start + Setpgid — and Setpgid only isolates the signal group, it does
// not change parentage). Restarting the server closed every master fd, every slave hit
// EOF, and every shell died. Users on tmux were spared only because tmux's own server
// setsid's itself out of the process tree. dw-muxd does for us what tmux's server does
// for tmux: it holds the PTYs somewhere that recompiling and restarting the HTTP layer
// cannot reach.
//
// The protocol below is the L1 contract. It is deliberately tiny and must stay stable:
// changing it means upgrading the daemon, and upgrading the daemon means dropping live
// sessions. Everything that wants to evolve freely — session names, titles, engines,
// tab order, layouts — travels as Meta, an OPAQUE byte string this package never
// interprets. That split is the whole reason the daemon can stay frozen while the
// product keeps moving.
package muxd

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// ProtoVersion is the wire contract version. Bump it ONLY for an incompatible change
// to the frame format or message set — a bump forces every live session to be dropped
// on upgrade (the client refuses to talk to a mismatched daemon rather than guessing).
//
// v2 (2026-08-22, pre-release): MsgDestroy became its own message type, and MsgKill now
// REFUSES signal 0. A v1 daemon does not know frame 22 and answers bad_frame, so deleting
// a tab would fail against one — a message-set change, which is exactly what this counter
// is for.
//
// It was worth spending a bump on, and the timing is the reason: the alternative was
// shipping v1 with "destroy" spelled as kill(pid, 0) — the Unix liveness probe — in a
// contract that never said so. That is a footgun a third-party implementation walks into
// exactly once, and after release the only ways out are the same bump at a far higher
// price (every user's live sessions) or living with it forever. Before release the cost
// is one `dw-terminal muxd --restart`, which now says what it will end and asks first.
const ProtoVersion = 2

// MsgType tags every frame. Control-plane messages carry JSON payloads; the two
// data-plane messages (MsgInput/MsgOutput) carry raw terminal bytes, because a PTY
// stream is high-frequency and encoding every chunk as JSON would tax the hot path
// for no benefit.
type MsgType uint8

const (
	// Handshake.
	MsgHello    MsgType = 1 // client → daemon: {"v":N}
	MsgHelloAck MsgType = 2 // daemon → client: {"v":N}
	MsgError    MsgType = 3 // either way: {"code":..,"msg":..}
	MsgOK       MsgType = 4 // daemon → client: empty ack for void operations

	// Control plane.
	MsgCreate    MsgType = 10
	MsgCreateAck MsgType = 11
	MsgList      MsgType = 12
	MsgListAck   MsgType = 13
	MsgAttach    MsgType = 14
	MsgAttachAck MsgType = 15
	MsgDetach    MsgType = 16
	MsgResize    MsgType = 17
	MsgKill      MsgType = 18
	MsgSetMeta   MsgType = 19
	MsgSubscribe MsgType = 20
	MsgEvent     MsgType = 21

	// MsgDestroy ends a session and forgets it. It is its OWN message type, and that is
	// a deliberate safety decision rather than tidiness.
	//
	// Destroy used to ride on MsgKill as the magic value Sig == 0. On Unix, kill(pid, 0)
	// is THE canonical way to ask "is this process still there" — it sends nothing. So
	// the single most destructive operation in this protocol was spelled exactly like a
	// liveness probe, in a contract file that never mentioned it. Any second
	// implementation reading this file would eventually probe a session and destroy it.
	// MsgKill now REFUSES Sig == 0 (bad_frame) so that mistake cannot be made silently.
	MsgDestroy MsgType = 22

	// MsgResized tells an ATTACHED client that the session's grid changed under it.
	//
	// It became necessary the moment size stopped being one client's private setting: with
	// smallest-wins, your terminal can be resized by someone else attaching, detaching, or
	// dragging their own window. A client that is never told renders every subsequent byte
	// onto the wrong grid — which for a TUI is not a stretched picture, it is a scrambled
	// one.
	MsgResized MsgType = 23

	// MsgGap tells an attached client that the daemon had to DROP output for it.
	//
	// Output is dropped rather than blocked on purpose — a slow client must never apply
	// backpressure to a shell — but the client cannot see it happen, and its own byte
	// accounting silently falls behind the ring. Re-attaching from that count then asks for
	// bytes it already has, so the scrollback duplicates instead of resuming.
	//
	// One empty frame is all it takes: the client stops trusting its resume point, takes a
	// full replay next time, and throws away a cache it can no longer prove contiguous. It
	// costs nothing on the hot path because it is only ever sent when something was already
	// lost.
	MsgGap MsgType = 24

	// Data plane — raw bytes, no JSON.
	MsgInput  MsgType = 30 // client → daemon: keystrokes for the attached session
	MsgOutput MsgType = 31 // daemon → client: PTY output for the attached session

	// MsgInputTo is the addressed form of MsgInput, for a connection that has not
	// attached (the HTTP fallback path posts keystrokes without opening a stream).
	// It is a separate type rather than an envelope on MsgInput so the attached hot
	// path stays raw: the common case must not pay for JSON on every keystroke.
	MsgInputTo MsgType = 33

	// Scrollback. Reading a session's history is a control-plane request/response like any
	// other; it deliberately does NOT ride the attached stream, because a client reads history
	// while scrolled back and its terminal keeps receiving live output at the same time.
	//
	// These did NOT cost a ProtoVersion bump, and that was the whole point of choosing the
	// capability mechanism over one: a bump refuses to talk to a mismatched daemon, which would
	// have ended every live session on this machine just to add a feature that nothing depends
	// on. Instead the daemon advertises FeatureScrollback and a client that does not see it
	// never sends these frames — so an old daemon is never asked a question it would answer
	// with bad_frame. See Hello.Features.
	MsgHistory          MsgType = 34
	MsgHistoryAck       MsgType = 35
	MsgHistorySearch    MsgType = 36
	MsgHistorySearchAck MsgType = 37

	// Stream-scoped lifecycle notice on an attached connection.
	MsgExited MsgType = 32 // daemon → client: {"exit_code":N}
)

// A deliberate omission, recorded so it is a decision rather than an oversight: frames
// carry NO request id, so an attached connection cannot correlate replies.
//
// The control connection does not need one — it is strictly request/response under a
// mutex. An attached stream is different: MsgOutput and MsgExited arrive unsolicited, so
// a MsgOK on that connection cannot be matched to the request that earned it. The
// consequence is real and accepted: Stream.Resize is fire-and-forget and stream-scoped
// failures cannot be reported. The alternative — an id on every frame — taxes the
// keystroke and output hot path forever to serve operations that are rare and already
// available on the control connection. If that trade ever needs revisiting, adding an
// optional id field is additive JSON, and a version mismatch already refuses to talk, so
// no mixed-version pair can be caught between the two schemes.

// MaxFrameSize bounds a single frame's payload. It exists so a corrupt or hostile
// length prefix cannot make the peer allocate unbounded memory: without it, four
// bytes of garbage translate directly into a multi-gigabyte make([]byte, n).
const MaxFrameSize = 16 << 20 // 16 MiB

// frameHeaderSize is 4 bytes of big-endian payload length + 1 byte of type.
const frameHeaderSize = 5

// ErrFrameTooLarge is returned when a frame's declared length exceeds MaxFrameSize.
var ErrFrameTooLarge = errors.New("muxd: frame exceeds max size")

// WriteFrame writes one length-prefixed frame. The length covers the payload only;
// the type byte lives in the header.
func WriteFrame(w io.Writer, t MsgType, payload []byte) error {
	if len(payload) > MaxFrameSize {
		return fmt.Errorf("%w: %d bytes", ErrFrameTooLarge, len(payload))
	}
	var hdr [frameHeaderSize]byte
	binary.BigEndian.PutUint32(hdr[0:4], uint32(len(payload)))
	hdr[4] = byte(t)
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if len(payload) == 0 {
		return nil
	}
	_, err := w.Write(payload)
	return err
}

// ReadFrame reads exactly one frame. A truncated frame yields io.ErrUnexpectedEOF
// (never a short, silently-accepted payload) — a half-read frame must be an error,
// not a smaller message that happens to parse.
func ReadFrame(r io.Reader) (MsgType, []byte, error) {
	var hdr [frameHeaderSize]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(hdr[0:4])
	t := MsgType(hdr[4])
	if n > MaxFrameSize {
		return 0, nil, fmt.Errorf("%w: declared %d bytes", ErrFrameTooLarge, n)
	}
	if n == 0 {
		return t, nil, nil
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		// io.ReadFull maps a partial read to ErrUnexpectedEOF; surface it as-is so
		// callers can distinguish "peer closed cleanly between frames" (io.EOF from
		// the header read above) from "peer died mid-frame".
		return 0, nil, err
	}
	return t, buf, nil
}

// WriteJSON marshals v and writes it as one frame of type t.
func WriteJSON(w io.Writer, t MsgType, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("muxd: marshal %d: %w", t, err)
	}
	return WriteFrame(w, t, b)
}

// ---------------------------------------------------------------------------
// Control-plane payloads.
// ---------------------------------------------------------------------------

// Hello is the first frame on every connection, in both directions — and it is the ONE
// payload in this protocol that must stay readable by every version, forever.
//
// Everything else here is free to change: an incompatible reshaping bumps ProtoVersion,
// and a mismatch is refused before a single other frame is exchanged. The handshake
// cannot use that escape hatch, because the handshake is what DETECTS the mismatch. So
// its shape is frozen: future versions may only ADD optional fields — never rename,
// retype, or remove one. A v9 client must still be able to read a v1 daemon's greeting
// well enough to tell the user what is running and what restarting it would cost.
//
// (Time is carried as unix seconds rather than time.Time for the same reason: an integer
// has one representation, forever, and `omitempty` actually works on it.)
type Hello struct {
	Version int `json:"v"`

	// Identity of the DAEMON, filled in only on its ack. A client's hello leaves these
	// zero, and a daemon older than a given field simply omits it.
	PID         int   `json:"pid,omitempty"`
	Sessions    int   `json:"sessions,omitempty"`
	StartedUnix int64 `json:"started,omitempty"`

	// Features is what this daemon can DO, as opposed to what shape its frames have.
	//
	// The version number cannot answer this question. It is deliberately not bumped for
	// wire-compatible additions — that is the promise that let a live daemon keep ten
	// sessions across an upgrade — so "same version" says the frames parse, and nothing
	// about whether the behaviour behind them is the same. A daemon built before
	// per-attachment geometry speaks v2 exactly as well as one built after it, routes the
	// resize somewhere else, and never says so. That silence is the failure mode: the
	// browser's size stops arriving and the only symptom is a corrupted screen inside a
	// program that is not at fault.
	//
	// Features closes it WITHOUT a version bump, and does so for daemons that predate the
	// field: they simply omit it, an absent capability reads as unsupported, and the client
	// can say which one is missing and what to do. Capabilities are additive and permanent
	// — a name here is a promise about behaviour, so it is never reused with a new meaning.
	Features []string `json:"features,omitempty"`
}

// Capability names. Additive and permanent: add one when behaviour a client can DEPEND on
// appears, never redefine an existing one.
const (
	// FeaturePerAttachmentGeometry: this daemon sizes a session from its clients'
	// per-attachment declarations (smallest-wins across attachments), honours a 0×0
	// withdrawal, and broadcasts the resulting grid. A daemon without it keeps one shared
	// size, so a viewer's size is silently dropped the moment anything else is attached.
	FeaturePerAttachmentGeometry = "per-attachment-geometry"

	// FeatureScrollback: this daemon keeps a per-session, line-based, colour-preserving
	// scrollback and answers MsgHistory / MsgHistorySearch.
	//
	// It is advertised but NOT required (see RequiredDaemonFeatures): a daemon without it runs
	// terminals perfectly well, it simply has no history to offer. That asymmetry is the point
	// — making it required would print an upgrade warning at every user whose daemon predates
	// the feature, for a capability nothing they are doing depends on, and the only cure for
	// that warning is a restart that ends all their sessions.
	FeatureScrollback = "scrollback"
)

// DaemonFeatures is what a daemon built from THIS source advertises.
var DaemonFeatures = []string{FeaturePerAttachmentGeometry, FeatureScrollback}

// RequiredDaemonFeatures is what a client built from this source needs the daemon to do.
//
// Separate from DaemonFeatures on purpose: they are equal today only because both halves
// ship together, and the whole problem being solved here is the day they do not.
var RequiredDaemonFeatures = []string{FeaturePerAttachmentGeometry}

// Has reports whether the daemon advertised a capability.
func (h Hello) Has(feature string) bool {
	for _, f := range h.Features {
		if f == feature {
			return true
		}
	}
	return false
}

// MissingFeatures lists what this client needs and the daemon did not advertise.
//
// Returns nil when nothing is missing, so a caller can branch on the slice itself. Note
// that a daemon predating the field is indistinguishable from one that supports nothing,
// which is the correct reading: neither can be relied on to do it.
func (h Hello) MissingFeatures() []string {
	var missing []string
	for _, want := range RequiredDaemonFeatures {
		if !h.Has(want) {
			missing = append(missing, want)
		}
	}
	return missing
}

// StartedAt reports when the daemon started, or the zero time if it did not say.
func (h Hello) StartedAt() time.Time {
	if h.StartedUnix == 0 {
		return time.Time{}
	}
	return time.Unix(h.StartedUnix, 0)
}

// MissedEvents reports whether the jump from last to got skipped anything.
//
// It lives beside the field it interprets so the producer's promise and the consumer's
// rule cannot drift apart, and so a second consumer inherits the reasoning rather than
// re-deriving it.
//
// got == 0 means a daemon too old to stamp events: nothing can be inferred, so nothing is.
//
// last == 0 is the FIRST event on a fresh subscription, and it is not exempt. Every
// subscription is numbered from 1, so a first event of 137 means 136 were lost before the
// consumer got started — which is not hypothetical: the subscribe happens, Restore takes
// its snapshot, and a burst can overflow the buffer before the loop begins draining.
// Treating the first event as a free baseline swallowed exactly that case, and because
// everything after it is contiguous, no later event could reveal it either.
func MissedEvents(last, got uint64) bool {
	if got == 0 {
		return false
	}
	if last == 0 {
		return got != 1
	}
	return got != last+1
}

// ErrorPayload is the daemon's refusal. Code is machine-readable; Msg is for humans.
type ErrorPayload struct {
	Code string `json:"code"`
	Msg  string `json:"msg"`
}

// Error codes. ErrCodeVersion in particular must stay stable: it is what a client
// shows the user as "the running daemon speaks a different protocol; restarting it
// will end N live sessions" instead of failing as an unreadable EOF.
const (
	ErrCodeVersion   = "version_mismatch"
	ErrCodeNotFound  = "not_found"
	ErrCodeBadFrame  = "bad_frame"
	ErrCodeInternal  = "internal"
	ErrCodeSpawnFail = "spawn_failed"
)

// CreateReq asks the daemon to spawn a PTY.
//
// Meta is deliberately []byte and NOT a struct or json.RawMessage: the daemon stores
// it and hands it back, and must never look inside. Every product-level field the UI
// cares about (name, title, engine, cwd label, tab order, split layout) rides in here,
// so those can change shape whenever the server likes without touching this contract
// and therefore without an upgrade that would kill live sessions.
type CreateReq struct {
	Argv []string `json:"argv"`
	Cwd  string   `json:"cwd"`
	Env  []string `json:"env,omitempty"`
	Cols uint16   `json:"cols"`
	Rows uint16   `json:"rows"`
	Meta []byte   `json:"meta,omitempty"`
}

// CreateAck returns the daemon-assigned session ID and the pid it spawned.
//
// ShellPID rides along because the caller always wants it and the daemon always has it
// at this exact moment. Without it every create was followed by a full List purely to
// look up one number — an extra round trip, scanning every session, to learn something
// the reply could simply have carried.
type CreateAck struct {
	ID       string `json:"id"`
	ShellPID int    `json:"shell_pid"`
}

// SessionSummary is one row of List. ShellPID is reported by the daemon because the
// server can no longer see the process itself — it must not be re-derived by walking
// the process tree.
// SessionSummary is one session as the daemon sees it.
//
// Alive is the authority on whether the process is still running, and ExitCode is only
// meaningful when it is false — while a session runs, ExitCode carries a -1 placeholder
// that means "not yet", not "exited with -1". Making it a pointer was considered and
// rejected: it would add a nil check at every read to express something Alive already
// says, and the one place the placeholder actually leaked (an exit announced on detach,
// carrying -1 for a live session) was a bug in the daemon, not in this representation.
type SessionSummary struct {
	ID        string    `json:"id"`
	Alive     bool      `json:"alive"`
	ExitCode  int       `json:"exit_code"`
	Cols      uint16    `json:"cols"`
	Rows      uint16    `json:"rows"`
	Meta      []byte    `json:"meta,omitempty"`
	ShellPID  int       `json:"shell_pid"`
	CreatedAt time.Time `json:"created_at"`

	// HistoryEnabled says whether this session keeps scrollback at all, and the rest describe
	// what it holds: HistoryLines is how many scrolled-off lines are available NOW, HistoryBytes
	// what they cost, HistoryBroken that the historian switched itself off after an internal error
	// (the terminal is unaffected — see History).
	//
	// Deliberately WITHOUT omitempty on the first three. `history_enabled: false` is the whole
	// point of the field, and `history_lines: 0` is a real answer ("nothing has scrolled off
	// yet"); omitempty deletes exactly those from the wire and leaves a reader unable to tell "no
	// history" from "this daemon is too old to have the field". This codebase has already paid for
	// that once. HistoryBroken keeps omitempty because there the NOTABLE value is true.
	//
	// A daemon predating these fields simply omits them, which decodes to enabled=false — honest
	// for a client's purposes: that daemon has no history to offer.
	HistoryEnabled bool  `json:"history_enabled"`
	HistoryLines   int64 `json:"history_lines"`
	HistoryBytes   int   `json:"history_bytes"`
	HistoryBroken  bool  `json:"history_broken,omitempty"`

	// Viewers is how many attachments declare a size — the ones that actually have a window
	// this session must fit. Attached counts every attachment, including OBSERVERS.
	//
	// Both numbers are reported because they answer different questions and the difference
	// is real: a server keeps an attachment open for every session in order to cache its
	// scrollback, whether or not any browser is showing it. Reporting only the total would
	// say "1 attached" for a session nobody has looked at in a week.
	Viewers int `json:"viewers,omitempty"`

	// Attached is how many clients are currently displaying this session.
	//
	// It became worth reporting the moment a second kind of client existed. "This tab is
	// not responding" has two completely different causes — the program inside is busy, or
	// nothing is attached and the output is going nowhere visible — and until this field
	// there was no way to tell them apart without guessing. It is also the number that
	// explains a surprising resize: with smallest-wins, a session shrinks because someone
	// ELSE is looking at it from a smaller window.
	Attached int `json:"attached,omitempty"`
}

// ListAck carries every session the daemon holds.
type ListAck struct {
	Sessions []SessionSummary `json:"sessions"`
}

// HistoryReq asks for a page of a session's scrollback.
//
// From/Count page through it; From below what is still held is clamped to the oldest line
// rather than refused, because the caller is scrolling and "here is the oldest I still have,
// and here is its number" is the useful answer.
//
// Screen asks for the live visible grid instead, numbered as if it continued the scrollback.
// A viewer needs both to render a continuous surface — without it there is a screen-sized hole
// between the last scrolled-off line and what the terminal is showing, and the client would
// have to guess its size.
type HistoryReq struct {
	ID     string `json:"id"`
	From   int64  `json:"from"`
	Count  int    `json:"count"`
	Screen bool   `json:"screen,omitempty"`

	// StylesFrom is how many style-table entries the client already has. The daemon returns
	// only the ones after it.
	//
	// Style ids are append-only and never reused for the life of a session, so a client
	// holding a PREFIX of the table can resolve every id it has ever been sent. That is what
	// makes the incremental transfer safe rather than merely smaller: there is no version to
	// get wrong and no invalidation to miss.
	StylesFrom int `json:"styles_from,omitempty"`
}

// HistoryAck is a page of scrollback plus the range it was taken from.
type HistoryAck struct {
	Lines []Line `json:"lines"`
	// Base is the oldest line still held and Total the number ever produced, so a client can
	// tell "you have reached the beginning" from "the beginning was evicted".
	Base  int64 `json:"base"`
	Total int64 `json:"total"`

	// Styles are the table entries from HistoryReq.StylesFrom onward; StylesTotal is the
	// table's full length, which is what the client passes back next time.
	Styles      []Style `json:"styles,omitempty"`
	StylesTotal int     `json:"styles_total"`

	// Enabled is false when this session keeps no scrollback at all; Broken that its historian
	// hit an internal error and switched itself off. Neither affects the terminal. Both are
	// reported rather than collapsed into "no lines", because a viewer that cannot tell them
	// apart from an empty history will tell the user the wrong thing.
	//
	// No omitempty on Enabled: false is the case worth transmitting.
	Enabled bool `json:"enabled"`
	Broken  bool `json:"broken,omitempty"`
}

// HistorySearchReq looks for text in a session's scrollback, IN THE DAEMON.
//
// Searching where the history lives is the difference between shipping one page of results and
// shipping fifty thousand lines to a browser so it can search them locally — which would spend
// exactly the bandwidth this whole design exists to save.
type HistorySearchReq struct {
	ID    string `json:"id"`
	Query string `json:"query"`
	// From is where to start scanning; Backward searches towards older lines, which is what a
	// person pressing "previous match" means.
	From       int64 `json:"from"`
	Backward   bool  `json:"backward,omitempty"`
	Limit      int   `json:"limit,omitempty"`
	IgnoreCase bool  `json:"ignore_case,omitempty"`
}

// HistoryMatch is one hit: which line, where in it, and the line itself for a preview list.
type HistoryMatch struct {
	N int64 `json:"n"`
	// Col is the BYTE offset of the match within Text, matching Span.Start's convention.
	Col  int    `json:"col"`
	Text string `json:"text"`
}

// HistorySearchAck carries the hits and the range that was searched.
type HistorySearchAck struct {
	Matches []HistoryMatch `json:"matches"`
	Base    int64          `json:"base"`
	Total   int64          `json:"total"`
	Enabled bool           `json:"enabled"`
}

// AttachReq turns the current connection into a stream bound to one session.
// Since, when non-nil, asks for scrollback replay from that byte offset; nil means
// "replay everything the ring still holds".
// AttachReq asks for a session's output stream.
//
// Cols/Rows declare the size of the terminal this attachment will be displayed in, and
// they are what makes geometry ownable: the session sizes itself to fit everyone watching
// (smallest wins), instead of to whoever called resize most recently.
//
// Leaving them ZERO means "I am an observer": watch, but impose no constraint. That is
// how a small window can look at a session without reflowing the one somebody else is
// working in — a distinction that does not exist if size is a single global setting.
type AttachReq struct {
	ID    string `json:"id"`
	Since *int64 `json:"since,omitempty"`
	Cols  uint16 `json:"cols,omitempty"`
	Rows  uint16 `json:"rows,omitempty"`
}

// ResizedPayload is the new grid, sent on an attached connection when it changes.
type ResizedPayload struct {
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

// AttachAck reports the session's geometry and where the replay stream ENDS, so a client
// can render the replay onto a correctly-sized grid and know its own resume point.
//
// Offset is the ring offset one byte PAST the replay — i.e. where the next live byte
// will land — and ReplayBytes is how much replay precedes it. A client's resume point is
// therefore `Offset - ReplayBytes` before it consumes the replay, and `Offset` after.
// This sentence exists because the previous one said "where the replay stream starts",
// which is the opposite; a second implementation written against it would have
// mis-tracked by the entire replay length on every re-attach.
//
// Cols/Rows are returned for a specific reason: the screen replay in the overview used
// to guess the geometry with a second hardcoded constant, and that guess is exactly how
// the replay drifted (see pty_manager.go's spawnCols/spawnRows comment). The size that
// travels with the scrollback is the only one that can be right.
type AttachAck struct {
	Cols   uint16 `json:"cols"`
	Rows   uint16 `json:"rows"`
	Offset int64  `json:"offset"`
	Alive  bool   `json:"alive"`

	// ReplayBytes is how many of the bytes about to arrive are HISTORY rather than live
	// output. Without it a client cannot tell the replay apart from what follows, and so
	// cannot track where it has read up to — which means a re-attach after a dropped
	// connection has to ask for the whole ring again and duplicates everything the user
	// already has on screen.
	ReplayBytes int64 `json:"replay_bytes"`
}

// SimpleReq addresses one session for operations that need nothing else.
type SimpleReq struct {
	ID string `json:"id"`
}

// ResizeReq changes a size. WHICH size depends on where it is sent, and the split is the
// whole point:
//
//   - On an ATTACHED connection it sets THAT attachment's size — "the window I am showing
//     this in is now this big". The session re-derives its own size from every attachment.
//   - On the control connection it sets the manual fallback, used only while no attachment
//     has declared anything. A caller that is not attached is not looking at the session
//     and cannot know what fits, so it does not get to overrule those who are.
type ResizeReq struct {
	ID   string `json:"id"`
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

// KillReq sends a signal to a session.
//
// Foreground targets the PTY's CURRENT foreground process group instead of the session
// leader — the "kill whatever is in front" recovery for a program that ignores Ctrl+C.
// It is a mode of Kill rather than a new operation because the daemon still just signals
// the session; only the choice of target differs. It has to live daemon-side at all
// because reading that group means TIOCGPGRP on the PTY master, and the master fd is
// exactly what the server no longer holds.
//
// Sig == 0 is REFUSED (bad_frame). It is the Unix liveness probe — it sends no signal —
// and it used to mean "destroy this session" here, which made the most destructive
// operation in the protocol indistinguishable from the most harmless one. Destroy is
// MsgDestroy.
type KillReq struct {
	ID         string `json:"id"`
	Sig        int    `json:"sig"`
	Foreground bool   `json:"foreground,omitempty"`
}

// SetMetaReq replaces a session's opaque metadata blob.
type SetMetaReq struct {
	ID   string `json:"id"`
	Meta []byte `json:"meta"`
}

// InputReq carries keystrokes on the CONTROL connection (the HTTP fallback path posts
// input without attaching). On an attached stream, input travels as a bare MsgInput
// frame with no envelope — that is the hot path and it stays raw.
type InputReq struct {
	ID   string `json:"id"`
	Data []byte `json:"data"`
}

// Event kinds emitted to subscribers.
//
// EventDestroyed is distinct from EventExited on purpose, and the distinction is
// load-bearing for a multi-host setup. "Exited" means the process ended — the session is
// still a real thing the other host should show, greyed out, with its scrollback intact.
// "Destroyed" means the user deleted the tab and it must disappear everywhere. Collapsing
// the two leaves the other host either resurrecting deleted tabs or discarding live
// scrollback. Before this existed, destroy propagated as nothing at all: host A's tab
// vanished, host B kept showing it forever.
//
// Consumers MUST tolerate unknown kinds (ignore + log), so future kinds stay additive.

// EventKind names a lifecycle transition. A named type rather than a bare string so that a
// switch over it reads as a closed set and a typo in a comparison is a compile error — while
// still being a string on the wire, because the openness is the point: a consumer meeting a
// kind from a newer daemon must ignore it, not fail.
type EventKind string

const (
	EventCreated     EventKind = "created"
	EventExited      EventKind = "exited"
	EventDestroyed   EventKind = "destroyed"
	EventMetaChanged EventKind = "meta-changed"
)

// Event is a lifecycle notification on a subscribed connection.
//
// Seq is what makes a LOST event detectable, and without it this stream is unsafe to
// build on. Events are dropped on purpose at two points — a subscriber that cannot keep
// up must never apply backpressure to the daemon, let alone to a shell — and a silent
// drop is indistinguishable from "nothing happened". The consequence was a view that
// drifted permanently: a burst of creates or deletes overflows a buffer, the events
// vanish, and nothing ever contradicts the stale picture.
//
// A per-subscription counter that only increases lets the receiver notice a gap in one
// comparison and answer it the only way that is actually correct: re-read the daemon's
// full list. So the events are a hint that something changed, the sequence is a hint that
// something was MISSED, and the authoritative answer is always a List.
type Event struct {
	Kind     EventKind `json:"kind"`
	ID       string    `json:"id"`
	Seq      uint64    `json:"seq,omitempty"`
	ExitCode int       `json:"exit_code,omitempty"`
	Meta     []byte    `json:"meta,omitempty"`
}

// ExitedPayload is sent on an attached stream when its session's process ends.
type ExitedPayload struct {
	ExitCode int `json:"exit_code"`
}

// ---------------------------------------------------------------------------
// Handshake.
// ---------------------------------------------------------------------------

// ProtocolError is a refusal that arrived intact from the daemon.
//
// It is a TYPE rather than a formatted string because callers must be able to branch on
// it. The difference between "no such session" and "the connection died" decides whether
// retrying could help — and a caller reduced to matching substrings of an error message
// will get that wrong the first time anyone rewords the message.
type ProtocolError struct {
	Code string
	Msg  string
}

func (e *ProtocolError) Error() string { return fmt.Sprintf("muxd: %s (%s)", e.Msg, e.Code) }

// IsNotFound reports whether err is the daemon saying the session does not exist.
//
// For a client holding a stale view this is definitive news, not a transient failure: the
// session is gone (its daemon died, or it was destroyed elsewhere) and no amount of
// retrying will bring it back. Treating it as transient is how a UI ends up showing
// ghost tabs that look alive and swallow every keystroke.
func IsNotFound(err error) bool {
	var pe *ProtocolError
	return errors.As(err, &pe) && pe.Code == ErrCodeNotFound
}

// VersionMismatchError reports that the peer speaks a different protocol version.
//
// It is a distinct type, and a RICH one, because of what it has to make possible. The
// scenario is the ordinary upgrade: the user installs a new dw-terminal while the old
// daemon is still resident, holding their live shells. The two cannot talk. The only
// remedy is to restart the daemon, and that ends every session it holds — so the user
// has to be told, in one sentence, exactly what is running and exactly what it costs.
// "protocol version mismatch" does not let anyone tell them that.
//
// Theirs is 0 when the daemon predates the greeting-carries-identity rule and refused
// with a bare error frame; DaemonPID is still filled in from the socket's peer
// credentials, which work against any version because they are not part of the protocol
// at all.
type VersionMismatchError struct {
	Ours        int
	Theirs      int
	DaemonPID   int
	Sessions    int
	StartedUnix int64
}

func (e *VersionMismatchError) Error() string {
	var b strings.Builder
	b.WriteString("muxd: the running session daemon speaks ")
	if e.Theirs > 0 {
		fmt.Fprintf(&b, "protocol v%d", e.Theirs)
	} else {
		b.WriteString("an older protocol")
	}
	fmt.Fprintf(&b, " and this build speaks v%d", e.Ours)
	if e.DaemonPID > 0 {
		fmt.Fprintf(&b, "; the daemon is pid %d", e.DaemonPID)
		if !e.StartedAt().IsZero() {
			fmt.Fprintf(&b, ", started %s", e.StartedAt().Format(time.RFC3339))
		}
	}
	switch {
	case e.Sessions > 0:
		fmt.Fprintf(&b, ", holding %d live session(s)", e.Sessions)
	case e.Theirs == 0:
		b.WriteString(", and it is too old to say how many sessions it holds")
	}
	b.WriteString(". Run `dw-terminal muxd --restart` to upgrade it — that ends every session it holds.")
	return b.String()
}

// StartedAt reports when the mismatched daemon started, or the zero time if unknown.
func (e *VersionMismatchError) StartedAt() time.Time {
	if e.StartedUnix == 0 {
		return time.Time{}
	}
	return time.Unix(e.StartedUnix, 0)
}

// ClientHandshake performs the client half: send Hello, expect HelloAck with the same
// version. A mismatch is refused loudly and explicitly — never negotiated down, never
// left to fail later as an unreadable EOF in the middle of some other operation.
//
// It returns the daemon's greeting even when the versions disagree: that greeting is the
// only place the caller can learn who is actually running and what stopping it costs.
func ClientHandshake(rw io.ReadWriter) (Hello, error) {
	if err := WriteJSON(rw, MsgHello, Hello{Version: ProtoVersion}); err != nil {
		return Hello{}, fmt.Errorf("muxd: send hello: %w", err)
	}
	t, payload, err := ReadFrame(rw)
	if err != nil {
		return Hello{}, fmt.Errorf("muxd: read hello ack: %w", err)
	}
	switch t {
	case MsgHelloAck:
		var ack Hello
		if err := json.Unmarshal(payload, &ack); err != nil {
			return Hello{}, fmt.Errorf("muxd: decode hello ack: %w", err)
		}
		if ack.Version != ProtoVersion {
			return ack, &VersionMismatchError{
				Ours: ProtoVersion, Theirs: ack.Version,
				DaemonPID: ack.PID, Sessions: ack.Sessions, StartedUnix: ack.StartedUnix,
			}
		}
		return ack, nil
	case MsgError:
		var e ErrorPayload
		if err := json.Unmarshal(payload, &e); err != nil {
			return Hello{}, fmt.Errorf("muxd: decode error frame: %w", err)
		}
		if e.Code == ErrCodeVersion {
			// A daemon old enough to refuse with an error frame instead of greeting with
			// its identity. Its version is only in the prose, which is not worth parsing;
			// the caller fills the pid in from peer credentials instead.
			return Hello{}, &VersionMismatchError{Ours: ProtoVersion, Theirs: 0}
		}
		return Hello{}, fmt.Errorf("muxd: daemon refused connection: %s (%s)", e.Msg, e.Code)
	default:
		return Hello{}, fmt.Errorf("muxd: unexpected frame %d during handshake", t)
	}
}

// ServerHandshake performs the daemon half: expect Hello, then reply with this daemon's
// identity — self, with Version forced to what this build actually speaks.
//
// It answers with a HelloAck even when the versions disagree, which is the whole point.
// Refusing with a bare error frame would be tidier and would tell the client nothing it
// could act on; a greeting says "I am pid P, I speak vN, I am holding M of your shells",
// and that is exactly the sentence the user needs before deciding to restart anything.
// The client is the one that then declines to continue.
func ServerHandshake(rw io.ReadWriter, self Hello) error {
	self.Version = ProtoVersion
	t, payload, err := ReadFrame(rw)
	if err != nil {
		return fmt.Errorf("muxd: read hello: %w", err)
	}
	if t != MsgHello {
		_ = WriteJSON(rw, MsgError, ErrorPayload{Code: ErrCodeBadFrame, Msg: "expected hello"})
		return fmt.Errorf("muxd: expected hello, got frame %d", t)
	}
	var h Hello
	if err := json.Unmarshal(payload, &h); err != nil {
		_ = WriteJSON(rw, MsgError, ErrorPayload{Code: ErrCodeBadFrame, Msg: "malformed hello"})
		return fmt.Errorf("muxd: decode hello: %w", err)
	}
	if err := WriteJSON(rw, MsgHelloAck, self); err != nil {
		return err
	}
	if h.Version != ProtoVersion {
		return &VersionMismatchError{
			Ours: ProtoVersion, Theirs: h.Version,
			DaemonPID: self.PID, Sessions: self.Sessions, StartedUnix: self.StartedUnix,
		}
	}
	return nil
}
