package ansisignal

import (
	"encoding/base64"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// The whole table is fed TWICE by TestFeed: once as a single chunk, and once split at every
// possible byte boundary. A PTY read boundary is arbitrary, so "works when the sequence is
// whole" proves almost nothing — the split run is the one that matters.
var cases = []struct {
	name string
	in   string
	want []Signal
}{
	{
		name: "plain text is silent",
		in:   "hello world\nsecond line\r\n",
	},
	{
		name: "bare BEL",
		in:   "ding\x07",
		want: []Signal{{Kind: KindBell}},
	},
	{
		name: "several bells",
		in:   "\x07\x07",
		want: []Signal{{Kind: KindBell}, {Kind: KindBell}},
	},
	{
		// The regression this guards: OSC bodies END with BEL. A window-title update happens
		// on virtually every shell prompt, so counting its terminator as a bell would light
		// up "needs you" continuously.
		name: "OSC 0 window title is not a bell",
		in:   "\x1b]0;~/code/deepwork\x07$ ",
	},
	{
		name: "OSC 2 title with ST terminator is not a bell",
		in:   "\x1b]2;title here\x1b\\ok",
	},
	{
		name: "OSC 9 body, BEL-terminated",
		in:   "\x1b]9;Build finished\x07",
		want: []Signal{{Kind: KindNotify, Body: "Build finished"}},
	},
	{
		name: "OSC 9 body, ST-terminated",
		in:   "\x1b]9;Build finished\x1b\\",
		want: []Signal{{Kind: KindNotify, Body: "Build finished"}},
	},
	{
		// ConEmu/Windows-Terminal multiplex progress + cwd reports onto OSC 9; some shell
		// integrations emit them every prompt. They are not notifications.
		name: "OSC 9;4 progress is ignored",
		in:   "\x1b]9;4;1;70\x07",
	},
	{
		name: "OSC 9;9 cwd report is ignored",
		in:   "\x1b]9;9;/home/user\x07",
	},
	{
		name: "OSC 9 empty body is ignored",
		in:   "\x1b]9;\x07",
	},
	{
		name: "OSC 777 notify with title and body",
		in:   "\x1b]777;notify;Claude Code;需要你的授权\x07",
		want: []Signal{{Kind: KindNotify, Title: "Claude Code", Body: "需要你的授权"}},
	},
	{
		name: "OSC 777 notify title only",
		in:   "\x1b]777;notify;Done\x1b\\",
		want: []Signal{{Kind: KindNotify, Title: "Done"}},
	},
	{
		name: "OSC 777 body keeps its own semicolons",
		in:   "\x1b]777;notify;T;a;b;c\x07",
		want: []Signal{{Kind: KindNotify, Title: "T", Body: "a;b;c"}},
	},
	{
		name: "OSC 777 non-notify subcommand is ignored",
		in:   "\x1b]777;precmd;whatever\x07",
	},
	{
		name: "OSC 99 kitty, default payload type is title",
		in:   "\x1b]99;;Agent needs you\x1b\\",
		want: []Signal{{Kind: KindNotify, Title: "Agent needs you"}},
	},
	{
		name: "OSC 99 kitty explicit body",
		in:   "\x1b]99;i=1:p=body;approve the edit?\x07",
		want: []Signal{{Kind: KindNotify, Body: "approve the edit?"}},
	},
	{
		name: "OSC 99 kitty base64 payload",
		in:   "\x1b]99;i=7:e=1;" + base64.StdEncoding.EncodeToString([]byte("完成了")) + "\x1b\\",
		want: []Signal{{Kind: KindNotify, Title: "完成了"}},
	},
	{
		name: "OSC 99 kitty chunked title then body",
		in: "\x1b]99;i=2:d=0;Cla\x1b\\" +
			"\x1b]99;i=2:d=0;ude\x1b\\" +
			"\x1b]99;i=2:p=body;waiting\x1b\\",
		want: []Signal{{Kind: KindNotify, Title: "Claude", Body: "waiting"}},
	},
	{
		name: "OSC 99 kitty undecodable base64 is dropped",
		in:   "\x1b]99;e=1;!!!not-base64!!!\x07",
	},
	{
		name: "unknown OSC number is ignored",
		in:   "\x1b]8;;https://example.com\x07link\x1b]8;;\x07",
	},
	{
		name: "OSC 52 clipboard payload is ignored",
		in:   "\x1b]52;c;aGVsbG8=\x07",
	},
	{
		// A DCS/APC payload is opaque (tmux passthrough, kitty graphics, sixel). Scanning it
		// would let an arbitrary 0x07 inside binary data masquerade as a bell.
		name: "BEL inside a DCS passthrough is not a bell",
		in:   "\x1bPtmux;\x07binary\x07\x1b\\after",
	},
	{
		name: "BEL inside an APC payload is not a bell",
		in:   "\x1b_Gf=100,a=T;pay\x07load\x1b\\",
	},
	{
		name: "signal after a skipped string still parses",
		in:   "\x1bPtmux;junk\x1b\\\x1b]9;done\x07",
		want: []Signal{{Kind: KindNotify, Body: "done"}},
	},
	{
		name: "CSI sequences are transparent",
		in:   "\x1b[1;31mred\x1b[0m\x1b[2J\x1b[H\x07",
		want: []Signal{{Kind: KindBell}},
	},
	{
		name: "malformed OSC aborted by a control char yields nothing",
		in:   "\x1b]777;notify;broken\nplain text",
	},
	{
		// ESC inside an OSC that is not ST aborts the OSC and starts a fresh sequence. The
		// scanner must not lose the following bell.
		name: "OSC aborted by a non-ST escape recovers",
		in:   "\x1b]9;half\x1b[0m\x07",
		want: []Signal{{Kind: KindBell}},
	},
	{
		name: "two notifications in one chunk",
		in:   "\x1b]9;one\x07text\x1b]777;notify;T;two\x1b\\",
		want: []Signal{
			{Kind: KindNotify, Body: "one"},
			{Kind: KindNotify, Title: "T", Body: "two"},
		},
	},
	{
		name: "bell and notification interleaved",
		in:   "\x07\x1b]9;n\x07\x07",
		want: []Signal{{Kind: KindBell}, {Kind: KindNotify, Body: "n"}, {Kind: KindBell}},
	},
	{
		// Over-long body: no signal, and — critically — the BEL that closes it is consumed
		// as a terminator, not reported as a bell.
		name: "oversized OSC body is dropped without a phantom bell",
		in:   "\x1b]9;" + strings.Repeat("x", MaxPendingBytes+512) + "\x07",
	},
	{
		name: "scanner recovers after an oversized body",
		in:   "\x1b]9;" + strings.Repeat("x", MaxPendingBytes+512) + "\x07\x1b]9;short\x07",
		want: []Signal{{Kind: KindNotify, Body: "short"}},
	},
	{
		name: "unterminated OSC never emits",
		in:   "\x1b]9;still going and going",
	},
	{
		name: "utf8 body survives",
		in:   "\x1b]777;notify;标题;正文内容\x07",
		want: []Signal{{Kind: KindNotify, Title: "标题", Body: "正文内容"}},
	},
}

func TestFeedWholeChunk(t *testing.T) {
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var s Scanner
			got := s.Feed([]byte(c.in))
			if !equalSignals(got, c.want) {
				t.Fatalf("Feed(%q) = %#v, want %#v", c.in, got, c.want)
			}
		})
	}
}

// TestFeedSplitAnywhere replays every case cut at every byte boundary. This is the property
// that actually matters in production: the kernel decides where a PTY read ends, so a
// sequence is regularly delivered in pieces.
func TestFeedSplitAnywhere(t *testing.T) {
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for cut := 0; cut <= len(c.in); cut++ {
				var s Scanner
				got := append([]Signal(nil), s.Feed([]byte(c.in[:cut]))...)
				got = append(got, s.Feed([]byte(c.in[cut:]))...)
				if !equalSignals(got, c.want) {
					t.Fatalf("split at %d: got %#v, want %#v", cut, got, c.want)
				}
			}
		})
	}
}

// TestFeedByteAtATime is the extreme of the same property: one byte per Feed call.
func TestFeedByteAtATime(t *testing.T) {
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var s Scanner
			var got []Signal
			for i := 0; i < len(c.in); i++ {
				got = append(got, s.Feed([]byte{c.in[i]})...)
			}
			if !equalSignals(got, c.want) {
				t.Fatalf("byte-at-a-time: got %#v, want %#v", got, c.want)
			}
		})
	}
}

func TestFeedEmptyAndNil(t *testing.T) {
	var s Scanner
	if got := s.Feed(nil); len(got) != 0 {
		t.Fatalf("Feed(nil) = %#v, want none", got)
	}
	if got := s.Feed([]byte{}); len(got) != 0 {
		t.Fatalf("Feed(empty) = %#v, want none", got)
	}
}

// TestFeedDoesNotRetainInput pins the pure-observer contract: the caller's buffer is reused
// by the PTY read loop, so a Scanner that kept a reference would report stale garbage.
func TestFeedDoesNotRetainInput(t *testing.T) {
	buf := []byte("\x1b]9;first\x07")
	var s Scanner
	got := s.Feed(buf)
	for i := range buf { // caller reuses the buffer for the next read
		buf[i] = 'z'
	}
	want := []Signal{{Kind: KindNotify, Body: "first"}}
	if !equalSignals(got, want) {
		t.Fatalf("after caller reused its buffer: got %#v, want %#v", got, want)
	}
}

// TestPendingIsBounded proves the memory ceiling: an OSC that never terminates cannot make
// the scanner grow past the cap, no matter how much is fed into it.
func TestPendingIsBounded(t *testing.T) {
	var s Scanner
	s.Feed([]byte("\x1b]9;"))
	for i := 0; i < 64; i++ {
		s.Feed([]byte(strings.Repeat("y", 4096)))
	}
	if len(s.pending) > MaxPendingBytes {
		t.Fatalf("pending grew to %d bytes, cap is %d", len(s.pending), MaxPendingBytes)
	}
}

// TestKittyPartialsAreBounded covers the other unbounded-growth door: chunked kitty
// notifications that are started under many identifiers and never finished.
func TestKittyPartialsAreBounded(t *testing.T) {
	var s Scanner
	for i := 0; i < 200; i++ {
		s.Feed([]byte("\x1b]99;i=" + strconv.Itoa(i) + ":d=0;chunk\x1b\\"))
	}
	if len(s.kitty) > maxKittyPartials {
		t.Fatalf("held %d partial kitty notifications, cap is %d", len(s.kitty), maxKittyPartials)
	}
}

// TestSkipResyncs pins the deafness escape hatch: a DCS that is opened and never terminated
// must not silence the scanner forever.
func TestSkipResyncs(t *testing.T) {
	var s Scanner
	s.Feed([]byte("\x1bP")) // opened, never terminated
	chunk := []byte(strings.Repeat("q", 64*1024))
	for fed := 0; fed <= maxSkipBytes; fed += len(chunk) {
		s.Feed(chunk)
	}
	got := s.Feed([]byte("\x1b]9;back\x07"))
	want := []Signal{{Kind: KindNotify, Body: "back"}}
	if !equalSignals(got, want) {
		t.Fatalf("after resync: got %#v, want %#v", got, want)
	}
}

func equalSignals(got, want []Signal) bool {
	if len(got) == 0 && len(want) == 0 {
		return true
	}
	return reflect.DeepEqual(got, want)
}
