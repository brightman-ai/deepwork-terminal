package terminal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
)

const clipboardHistoryLimit = 100
const clipboardHistoryBytes = 20 << 20

type clipboardEntry struct {
	ID        string    `json:"id"`
	Sequence  uint64    `json:"sequence"`
	SessionID string    `json:"sessionId"`
	Source    string    `json:"source"`
	Text      string    `json:"text"`
	Direction string    `json:"direction"`
	At        time.Time `json:"at"`
	Replayed  bool      `json:"replayed,omitempty"`
	TargetID  string    `json:"targetId,omitempty"`
	Action    string    `json:"action,omitempty"`
}
type clipboardStore struct {
	mu       sync.Mutex
	epoch    string
	sequence uint64
	entries  []clipboardEntry
	bytes    int
	changed  chan struct{}
}

func newClipboardStore() *clipboardStore {
	return &clipboardStore{epoch: uuid.NewString(), changed: make(chan struct{})}
}
func (c *clipboardStore) add(e clipboardEntry) clipboardEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, old := range c.entries {
		if old.ID == e.ID {
			return old
		}
	}
	c.sequence++
	e.Sequence = c.sequence
	if e.At.IsZero() {
		e.At = time.Now()
	}
	c.entries = append(c.entries, e)
	close(c.changed)
	c.changed = make(chan struct{})
	c.bytes += len(e.Text)
	for len(c.entries) > clipboardHistoryLimit || c.bytes > clipboardHistoryBytes {
		c.bytes -= len(c.entries[0].Text)
		c.entries = c.entries[1:]
	}
	return e
}

// Wait on the same lock as add: a copy between checking the cursor and waiting
// cannot be missed. Long polling delivers selection copies without a timer delay.
func (c *clipboardStore) wait(ctx context.Context, epoch string, since uint64) {
	c.mu.Lock()
	changed, pending := c.changed, epoch != c.epoch || since != c.sequence
	c.mu.Unlock()
	if pending {
		return
	}
	select {
	case <-changed:
	case <-ctx.Done():
	}
}
func (c *clipboardStore) snapshot(epoch string, since uint64) (string, uint64, []clipboardEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]clipboardEntry, 0)
	for _, e := range c.entries {
		if time.Since(e.At) < 24*time.Hour && (epoch != c.epoch || e.Sequence > since) {
			out = append(out, e)
		}
	}
	return c.epoch, c.sequence, out
}
func (s *Server) onClipboard(sess *Session, text string, offset int64, replayed bool) {
	sess.mu.Lock()
	name := sess.Name
	sess.mu.Unlock()
	s.clipboard.add(clipboardEntry{ID: fmt.Sprintf("%s:%d", sess.ID, offset), SessionID: sess.ID, Source: name, Text: text, Direction: "remote", Replayed: replayed})
}

// ClipboardBufferPath is shared with the local CLI. The path contains no user
// supplied path components, and buffers are private to this server's data directory.
func ClipboardBufferPath(dataDir, target string) string {
	sum := sha256.Sum256([]byte(target))
	return filepath.Join(dataDir, "clipboard", hex.EncodeToString(sum[:])+".txt")
}
func writeClipboardBuffer(dataDir, target, text string) error {
	path := ClipboardBufferPath(dataDir, target)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".clipboard-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.WriteString(text); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
