package muxd

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// TestProtoFrameRoundTrip covers the payload sizes that actually break framing code:
// empty (the length prefix is the whole message), one byte, and a payload far larger
// than any single Read will return — the case where a naive implementation silently
// truncates instead of looping.
func TestProtoFrameRoundTrip(t *testing.T) {
	big := bytes.Repeat([]byte("dwmux"), 300_000) // 1.5 MB, guaranteed multi-read
	cases := []struct {
		name    string
		msgType MsgType
		payload []byte
	}{
		{"empty", MsgList, nil},
		{"one byte", MsgInput, []byte{0x1b}},
		{"small json", MsgHello, []byte(`{"v":1}`)},
		{"large multi-read", MsgOutput, big},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := WriteFrame(&buf, tc.msgType, tc.payload); err != nil {
				t.Fatalf("WriteFrame: %v", err)
			}
			gotType, gotPayload, err := ReadFrame(&buf)
			if err != nil {
				t.Fatalf("ReadFrame: %v", err)
			}
			if gotType != tc.msgType {
				t.Errorf("type = %d, want %d", gotType, tc.msgType)
			}
			if !bytes.Equal(gotPayload, tc.payload) {
				t.Errorf("payload len = %d, want %d", len(gotPayload), len(tc.payload))
			}
			if buf.Len() != 0 {
				t.Errorf("%d bytes left unconsumed after one frame", buf.Len())
			}
		})
	}
}

// TestProtoFramesBackToBack proves the reader consumes exactly one frame at a time:
// a stream is a sequence of frames, and a reader that over-reads eats the next one.
func TestProtoFramesBackToBack(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteFrame(&buf, MsgInput, []byte("first")); err != nil {
		t.Fatalf("write 1: %v", err)
	}
	if err := WriteFrame(&buf, MsgOutput, []byte("second")); err != nil {
		t.Fatalf("write 2: %v", err)
	}
	for _, want := range []struct {
		typ  MsgType
		data string
	}{{MsgInput, "first"}, {MsgOutput, "second"}} {
		typ, data, err := ReadFrame(&buf)
		if err != nil {
			t.Fatalf("ReadFrame: %v", err)
		}
		if typ != want.typ || string(data) != want.data {
			t.Errorf("got (%d,%q), want (%d,%q)", typ, data, want.typ, want.data)
		}
	}
	if _, _, err := ReadFrame(&buf); !errors.Is(err, io.EOF) {
		t.Errorf("after last frame err = %v, want io.EOF", err)
	}
}

// TestProtoTruncatedFrameIsError is the anti-silent-corruption case: half a frame must
// be an error, never a shorter message that happens to decode.
func TestProtoTruncatedFrameIsError(t *testing.T) {
	var full bytes.Buffer
	if err := WriteFrame(&full, MsgOutput, []byte("0123456789")); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	raw := full.Bytes()

	t.Run("truncated payload", func(t *testing.T) {
		cut := bytes.NewReader(raw[:len(raw)-4]) // header intact, payload short
		_, _, err := ReadFrame(cut)
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Errorf("err = %v, want io.ErrUnexpectedEOF", err)
		}
	})

	t.Run("truncated header", func(t *testing.T) {
		cut := bytes.NewReader(raw[:3]) // less than frameHeaderSize
		_, _, err := ReadFrame(cut)
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Errorf("err = %v, want io.ErrUnexpectedEOF", err)
		}
	})
}

// TestProtoOversizeFrameRejected guards the allocation path: four bytes of garbage in
// the length prefix must not turn into a multi-gigabyte make([]byte, n).
func TestProtoOversizeFrameRejected(t *testing.T) {
	t.Run("read side", func(t *testing.T) {
		var hdr [frameHeaderSize]byte
		binary.BigEndian.PutUint32(hdr[0:4], uint32(MaxFrameSize+1))
		hdr[4] = byte(MsgOutput)
		_, _, err := ReadFrame(bytes.NewReader(hdr[:]))
		if !errors.Is(err, ErrFrameTooLarge) {
			t.Errorf("err = %v, want ErrFrameTooLarge", err)
		}
	})

	t.Run("write side", func(t *testing.T) {
		var buf bytes.Buffer
		err := WriteFrame(&buf, MsgOutput, make([]byte, MaxFrameSize+1))
		if !errors.Is(err, ErrFrameTooLarge) {
			t.Errorf("err = %v, want ErrFrameTooLarge", err)
		}
		if buf.Len() != 0 {
			t.Errorf("wrote %d bytes for a rejected frame, want 0", buf.Len())
		}
	})
}

// TestProtoMetaStaysOpaque is the machine-checkable form of the L1 rule that keeps the
// daemon upgradeable: Meta is bytes in and the SAME bytes out. If it ever became a
// typed struct or got re-marshalled, arbitrary server-side payloads would stop
// surviving the round trip — and schema evolution would start requiring daemon
// upgrades, which cost live sessions.
func TestProtoMetaStaysOpaque(t *testing.T) {
	// Deliberately NOT valid JSON: the daemon must not care.
	meta := []byte{0x00, 0xff, 'n', 'o', 't', ' ', 'j', 's', 'o', 'n', 0x7f}
	var buf bytes.Buffer
	if err := WriteJSON(&buf, MsgCreate, CreateReq{Argv: []string{"/bin/sh"}, Meta: meta}); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	_, payload, err := ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	var got CreateReq
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !bytes.Equal(got.Meta, meta) {
		t.Errorf("meta round trip = %v, want %v", got.Meta, meta)
	}
}

// testIdentity is the greeting a daemon under test introduces itself with. The values
// are arbitrary but distinctive, so an assertion that they arrived cannot pass by
// accident on a zero value.
var testIdentity = Hello{PID: 424242, Sessions: 7, StartedUnix: 1700000000}

// pipeHandshake runs the two handshake halves against each other over net.Pipe and
// returns (daemonGreeting, clientErr, serverErr). clientVersion overrides what the
// client claims to speak so the mismatch path can be exercised.
func pipeHandshake(t *testing.T, clientVersion int) (Hello, error, error) {
	t.Helper()
	c, s := net.Pipe()
	defer c.Close()
	defer s.Close()
	deadline := time.Now().Add(5 * time.Second)
	_ = c.SetDeadline(deadline)
	_ = s.SetDeadline(deadline)

	srvErr := make(chan error, 1)
	go func() { srvErr <- ServerHandshake(s, testIdentity) }()

	var (
		greeting Hello
		cliErr   error
	)
	if clientVersion == ProtoVersion {
		greeting, cliErr = ClientHandshake(c)
	} else {
		// Hand-roll the client half so it announces a version we do not support. The
		// daemon must still GREET it — that greeting is the only way a mismatched client
		// can learn what is running and what restarting it would cost.
		if err := WriteJSON(c, MsgHello, Hello{Version: clientVersion}); err != nil {
			t.Fatalf("send hello: %v", err)
		}
		typ, payload, err := ReadFrame(c)
		if err != nil {
			t.Fatalf("read reply: %v", err)
		}
		if typ != MsgHelloAck {
			t.Fatalf("reply frame = %d, want MsgHelloAck(%d) — a bare refusal tells a "+
				"mismatched client nothing it can act on", typ, MsgHelloAck)
		}
		if err := json.Unmarshal(payload, &greeting); err != nil {
			t.Fatalf("decode hello ack: %v", err)
		}
		cliErr = errors.New("client version not supported")
	}
	return greeting, cliErr, <-srvErr
}

// TestVersionHandshakeAgree is the happy path.
func TestVersionHandshakeAgree(t *testing.T) {
	greeting, cliErr, srvErr := pipeHandshake(t, ProtoVersion)
	if cliErr != nil {
		t.Errorf("client handshake: %v", cliErr)
	}
	if srvErr != nil {
		t.Errorf("server handshake: %v", srvErr)
	}
	// The greeting carries the daemon's identity on the happy path too, not only on
	// mismatch — that is what lets `muxd --status` and the server's own logs name the
	// process holding the sessions without a second round trip.
	if greeting.PID != testIdentity.PID || greeting.Sessions != testIdentity.Sessions {
		t.Errorf("greeting = %+v, want pid %d and %d sessions",
			greeting, testIdentity.PID, testIdentity.Sessions)
	}
}

// TestVersionHandshakeMismatchIsRefusedLoudly is the reverse-verification case that
// matters most: an incompatible daemon must refuse with a typed, readable error. A
// silent downgrade — or a bare EOF surfacing later inside some unrelated operation —
// is the failure mode this test exists to make impossible.
func TestVersionHandshakeMismatchIsRefusedLoudly(t *testing.T) {
	greeting, cliErr, srvErr := pipeHandshake(t, ProtoVersion+1)
	if cliErr == nil {
		t.Fatal("client saw no error on version mismatch — a downgrade happened silently")
	}
	var vErr *VersionMismatchError
	if !errors.As(srvErr, &vErr) {
		t.Fatalf("server err = %v, want *VersionMismatchError", srvErr)
	}
	if vErr.Theirs != ProtoVersion+1 || vErr.Ours != ProtoVersion {
		t.Errorf("mismatch = ours %d theirs %d, want ours %d theirs %d",
			vErr.Ours, vErr.Theirs, ProtoVersion, ProtoVersion+1)
	}

	// The half that decides whether the USER can act. A mismatch is only ever resolved by
	// restarting the daemon, which ends every shell it holds — so the mismatched peer must
	// come away knowing which process is holding them and how many there are. If this
	// greeting were withheld (or reduced to prose in an error frame), the best message the
	// product could ever show is "something incompatible is running", and the user would
	// have to guess at the cost of fixing it.
	if greeting.Version != ProtoVersion {
		t.Errorf("greeting version = %d, want %d", greeting.Version, ProtoVersion)
	}
	if greeting.PID != testIdentity.PID {
		t.Errorf("greeting pid = %d, want %d — the mismatched client cannot name the process holding its sessions",
			greeting.PID, testIdentity.PID)
	}
	if greeting.Sessions != testIdentity.Sessions {
		t.Errorf("greeting sessions = %d, want %d — the restart warning cannot say what it costs",
			greeting.Sessions, testIdentity.Sessions)
	}
	if !greeting.StartedAt().Equal(time.Unix(testIdentity.StartedUnix, 0)) {
		t.Errorf("greeting startedAt = %v, want %v", greeting.StartedAt(), time.Unix(testIdentity.StartedUnix, 0))
	}
}

// TestVersionMismatchErrorIsActionable pins the SENTENCE, not just the type.
//
// A typed error nobody can read is only half the fix: the whole point of carrying pid and
// session count through the handshake is that the message can name them. This asserts the
// rendered text contains what a user needs to decide — which process, how many sessions,
// and the exact command that resolves it.
func TestVersionMismatchErrorIsActionable(t *testing.T) {
	err := &VersionMismatchError{Ours: 2, Theirs: 1, DaemonPID: 4242, Sessions: 7,
		StartedUnix: 1700000000}
	msg := err.Error()
	for _, want := range []string{"v1", "v2", "4242", "7 live session", "muxd --restart"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q does not mention %q", msg, want)
		}
	}

	// And the degraded case — a daemon too old to introduce itself — must still be honest
	// rather than inventing a session count of zero.
	old := (&VersionMismatchError{Ours: 2, Theirs: 0, DaemonPID: 99}).Error()
	if strings.Contains(old, "0 live session") {
		t.Errorf("unknown session count rendered as zero: %q", old)
	}
	if !strings.Contains(old, "too old to say") {
		t.Errorf("message %q does not admit that the session count is unknown", old)
	}
}

// TestVersionHandshakeRejectsNonHelloFirstFrame: the first frame must be a Hello.
// Anything else is refused rather than being processed on an unnegotiated connection.
func TestVersionHandshakeRejectsNonHelloFirstFrame(t *testing.T) {
	c, s := net.Pipe()
	defer c.Close()
	defer s.Close()
	deadline := time.Now().Add(5 * time.Second)
	_ = c.SetDeadline(deadline)
	_ = s.SetDeadline(deadline)

	srvErr := make(chan error, 1)
	go func() { srvErr <- ServerHandshake(s, testIdentity) }()

	if err := WriteJSON(c, MsgList, struct{}{}); err != nil {
		t.Fatalf("send list: %v", err)
	}
	typ, payload, err := ReadFrame(c)
	if err != nil {
		t.Fatalf("read reply: %v", err)
	}
	if typ != MsgError {
		t.Fatalf("reply = %d, want MsgError", typ)
	}
	var e ErrorPayload
	if err := json.Unmarshal(payload, &e); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if e.Code != ErrCodeBadFrame {
		t.Errorf("code = %q, want %q", e.Code, ErrCodeBadFrame)
	}
	if err := <-srvErr; err == nil {
		t.Error("server accepted a non-hello first frame")
	}
}
