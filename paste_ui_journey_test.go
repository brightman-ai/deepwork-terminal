//go:build uijourney

package terminal

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
)

// No connection to the user's daemon/PTY. The real socket, real PTY, production
// server and frontend drive an isolated paste-consuming terminal program.
func TestIsolatedPasteUIJourneyServer(t *testing.T) {
	root := os.Getenv("VM_CLI_JOURNEY_ROOT")
	if root == "" {
		t.Fatal("explicit isolated root required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(root) || filepath.Base(abs) != "cli-ui-data" {
		t.Fatal("unexpected fixture root")
	}
	if err := os.MkdirAll(abs, 0700); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(abs, "paste_fixture.py")
	source := `import os,sys,tty,json,signal
from pathlib import Path
tty.setraw(0)
print('\x1b[?2004h\r\nPASTE FIXTURE READY (isolated)\r',flush=True)
signal.signal(signal.SIGWINCH,lambda *args:None)
pending=b''
transactions=0
while True:
 data=os.read(0,65536)
 if not data:break
 pending+=data
 while b'\x1b[201~' in pending:
  start=pending.find(b'\x1b[200~');end=pending.find(b'\x1b[201~')
  if start<0 or end<start:
   pending=pending[end+6:];continue
  payload=pending[start+6:end].decode('utf-8')
  pending=pending[end+6:]
  transactions+=1
  receipt={'transactions':transactions,'text':payload,'chars':len(payload)}
  Path(sys.argv[1]).write_text(json.dumps(receipt,ensure_ascii=False))
  print('\r\nPASTE #%d: %s\r\n'%(transactions,payload.replace('\n',' / ').replace('\r',' / ')),flush=True)
`
	if err := os.WriteFile(script, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(abs, "fixture-shell")
	command := fmt.Sprintf("#!/bin/sh\nexec /usr/bin/python3 '%s' '%s'\n", script, filepath.Join(abs, "paste-receipt.json"))
	if err := os.WriteFile(wrapper, []byte(command), 0700); err != nil {
		t.Fatal(err)
	}
	manager := newRealPTYManager(t, 1<<16, wrapper)
	manager.SetOrigin(abs)
	session, err := manager.CreateWithOptions(CreateOptions{Name: "粘贴验收（隔离）", CWD: abs})
	if err != nil {
		t.Fatal(err)
	}
	// Initial fixture creation starts its in-process daemon before production
	// NewServer.Restore, so restore can only see this test socket.
	server, err := NewServer(WithConfig(Config{Addr: "127.0.0.1:34218", DataDir: abs, AuthCode: "82504613", DefaultShell: wrapper, BufferSize: 1 << 16, MaxSessions: 2}))
	if err != nil {
		t.Fatal(err)
	}
	// The sanctioned test seam replaces only the external PTY hosting owner;
	// every HTTP/WS/paste handler stays production code.
	_ = server.mgr.CloseAll()
	server.mgr = manager
	manager.OnClipboard = server.onClipboard
	t.Cleanup(func() { server.Close() })
	_ = session
	fmt.Println("CLI_JOURNEY_READY http://127.0.0.1:34218 fixture_only=true native=false")
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := server.ListenAndServe(ctx); err != nil && err != http.ErrServerClosed {
		t.Fatal(err)
	}
}
