package terminal

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRemoteClipboardScannerFraming(t *testing.T) {
	text := "业务基线🙂\tline two\n"
	for _, end := range []string{"\a", "\x1b\\"} {
		wire := "screen\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + end
		for split := 0; split <= len(wire); split++ {
			s := clipboardScanner{offset: 123}
			var got []clipboardEntry
			emit := func(text string, offset int64, replayed bool) {
				got = append(got, clipboardEntry{Text: text, ID: fmt.Sprint(offset), Replayed: replayed})
			}
			s.feed([]byte(wire[:split]), false, emit)
			s.feed([]byte(wire[split:]), false, emit)
			require.Equal(t, []clipboardEntry{{Text: text, ID: fmt.Sprint(123 + len(wire))}}, got, "split %d", split)
		}
	}
	s := clipboardScanner{}
	var got []bool
	emit := func(_ string, _ int64, old bool) { got = append(got, old) }
	s.feed([]byte("\x1b]52;c;a"), true, emit)
	s.feed([]byte("Gk=\a\x1b]52;c;bmV3\a"), false, emit)
	require.Equal(t, []bool{true, false}, got, "sequence starting in history must never re-copy")
}

func TestRemoteClipboardScannerRejectsAndRecovers(t *testing.T) {
	s := clipboardScanner{}
	var got []string
	emit := func(text string, _ int64, _ bool) { got = append(got, text) }
	wire := "\x1b]52;c;?\a\x1b]52;c;%%%\a\x1b]52;c;/w==\a\x1b]52;bad;aGk=\a\x1b]52;c;\a"
	wire += "\x1b]52;c;" + strings.Repeat("A", ((clipboardMaxBytes+2)/3)*4+100) + "\a\x1b]52;c;aGk=\a"
	s.feed([]byte(wire), false, emit)
	require.Equal(t, []string{"hi"}, got)
	var modes []bool
	s = clipboardScanner{pasteMode: func(on bool) { modes = append(modes, on) }}
	for _, b := range []byte("\x1b[?2004h\x1b[?25;2004l\x1b]0;title [ ?2004h\a") {
		s.feed([]byte{b}, false, emit)
	}
	require.Equal(t, []bool{true, false}, modes)
}

func TestRemoteClipboardCursorBoundsAndWakeup(t *testing.T) {
	s := newClipboardStore()
	epoch, cursor, entries := s.snapshot("", 0)
	require.Empty(t, entries)
	done := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() { s.wait(ctx, epoch, cursor); close(done) }()
	e := s.add(clipboardEntry{ID: "one", Text: "content"})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("copy did not wake waiting browser")
	}
	require.Equal(t, e, s.add(clipboardEntry{ID: "one", Text: "duplicate"}))
	_, cursor, entries = s.snapshot(epoch, e.Sequence)
	require.Empty(t, entries)
	require.Equal(t, e.Sequence, cursor)
	_, _, entries = s.snapshot("previous-server", cursor)
	require.Len(t, entries, 1)
	for i := 0; i < 110; i++ {
		s.add(clipboardEntry{ID: fmt.Sprint(i), Text: "x"})
	}
	_, _, entries = s.snapshot("", 0)
	require.Len(t, entries, 100)
	require.Equal(t, "10", entries[0].ID)
	for i := 0; i < 6; i++ {
		s.add(clipboardEntry{ID: fmt.Sprintf("large-%d", i), Text: strings.Repeat("x", clipboardMaxBytes)})
	}
	_, _, entries = s.snapshot("", 0)
	require.Len(t, entries, 5)
	require.LessOrEqual(t, s.bytes, clipboardHistoryBytes)
}

func TestRemoteClipboardSaveAndConfirmedSend(t *testing.T) {
	sm := newRealPTYManager(t, 1<<16, "/bin/sh")
	srv := &Server{mgr: sm, config: Config{DataDir: t.TempDir()}, clipboard: newClipboardStore()}
	sess, err := sm.CreateWithOptions(CreateOptions{Name: "clipboard fixture", CWD: t.TempDir()})
	require.NoError(t, err)
	targets, err := srv.clipboardTargets(context.Background(), sess.ID)
	require.NoError(t, err)
	require.Len(t, targets, 1)
	marker := filepath.Join(t.TempDir(), "sent")
	text := "printf 'copied exactly' > " + shellQuoteClipboard(marker)
	call := func(path string, target clipboardTarget, text string) *httptest.ResponseRecorder {
		b, _ := json.Marshal(clipboardWrite{Target: target, Text: text})
		req := httptest.NewRequest("POST", path, bytes.NewReader(b))
		w := httptest.NewRecorder()
		if path == "/clipboard" {
			srv.handleClipboardSave(w, req)
		} else {
			srv.handleClipboardSend(w, req)
		}
		return w
	}
	require.Equal(t, 200, call("/clipboard", targets[0], text).Code)
	_, err = os.Stat(marker)
	require.True(t, os.IsNotExist(err), "saving must not execute in the shell")
	stored, err := os.ReadFile(ClipboardBufferPath(srv.config.DataDir, targets[0].ID))
	require.NoError(t, err)
	require.Equal(t, text, string(stored))
	info, err := os.Stat(ClipboardBufferPath(srv.config.DataDir, targets[0].ID))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	stale := targets[0]
	stale.PID++
	require.Equal(t, 409, call("/clipboard/send", stale, text).Code)
	require.Equal(t, 400, call("/clipboard/send", targets[0], "\x1b[31m").Code)
	require.Equal(t, 200, call("/clipboard/send", targets[0], text).Code)
	require.Eventually(t, func() bool { b, _ := os.ReadFile(marker); return string(b) == "copied exactly" }, 3*time.Second, 10*time.Millisecond, "plain sh must not receive unsupported bracketed-paste bytes")
	_, _, entries := srv.clipboard.snapshot("", 0)
	require.Len(t, entries, 2)
	require.Equal(t, "已发送到目标终端", entries[1].Action)
}

func TestRemoteClipboardObservedWithoutBrowserAndReplay(t *testing.T) {
	sm := newRealPTYManager(t, 1<<16, "/bin/sh")
	srv := &Server{clipboard: newClipboardStore()}
	sm.OnClipboard = srv.onClipboard
	sess, err := sm.CreateWithOptions(CreateOptions{Name: "no browser", CWD: t.TempDir()})
	require.NoError(t, err)
	require.NoError(t, sess.WriteInput([]byte("printf '\\033]52;c;dGV4dA==\\007'\n")))
	require.Eventually(t, func() bool { _, _, e := srv.clipboard.snapshot("", 0); return len(e) == 1 }, 3*time.Second, 10*time.Millisecond)
	_, _, before := srv.clipboard.snapshot("", 0)
	require.Equal(t, "text", before[0].Text)
	require.False(t, before[0].Replayed)
	sm.CloseAll()
	restored := NewSessionManager(1<<16, "/bin/sh")
	restored.muxSocket = sm.muxSocket
	t.Cleanup(restored.DestroyAll)
	next := &Server{clipboard: newClipboardStore()}
	restored.OnClipboard = next.onClipboard
	require.NoError(t, restored.Restore())
	require.Eventually(t, func() bool { _, _, e := next.clipboard.snapshot("", 0); return len(e) == 1 }, 3*time.Second, 10*time.Millisecond)
	_, _, after := next.clipboard.snapshot("", 0)
	require.Equal(t, before[0].ID, after[0].ID)
	require.True(t, after[0].Replayed)
}
