package terminal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/creack/pty"
)

// InProcessService wraps SessionManager and implements TerminalSessionService.
// All calls are in-process; no network hop occurs.  SessionManager is already
// thread-safe via sync.Map, so InProcessService inherits that guarantee.
type InProcessService struct {
	manager *SessionManager
}

// NewInProcessService creates an InProcessService backed by the given manager.
func NewInProcessService(manager *SessionManager) *InProcessService {
	return &InProcessService{manager: manager}
}

// List implements TerminalSessionService.
func (s *InProcessService) List(_ context.Context) ([]SessionInfo, error) {
	sessions := s.manager.List()
	result := make([]SessionInfo, 0, len(sessions))
	for _, sess := range sessions {
		result = append(result, sessionToInfo(sess))
	}
	return result, nil
}

// Create implements TerminalSessionService.
func (s *InProcessService) Create(_ context.Context, opts CreateOptions) (*SessionInfo, error) {
	sess, err := s.manager.CreateWithOptions(opts)
	if err != nil {
		return nil, err
	}
	info := sessionToInfo(sess)
	return &info, nil
}

// Get implements TerminalSessionService.
func (s *InProcessService) Get(_ context.Context, id string) (*SessionInfo, error) {
	sess, err := s.manager.Get(id)
	if err != nil {
		return nil, err
	}
	info := sessionToInfo(sess)
	return &info, nil
}

// Delete implements TerminalSessionService.
func (s *InProcessService) Delete(_ context.Context, id string) error {
	return s.manager.Destroy(id)
}

// Resize implements TerminalSessionService.
func (s *InProcessService) Resize(_ context.Context, id string, cols, rows int) error {
	if cols < 1 || rows < 1 || cols > 500 || rows > 500 {
		return fmt.Errorf("resize: cols/rows out of bounds (%d×%d)", cols, rows)
	}
	sess, err := s.manager.Get(id)
	if err != nil {
		return err
	}
	sess.mu.Lock()
	ptyFile := sess.PTY
	sess.mu.Unlock()
	if ptyFile == nil {
		return fmt.Errorf("session %s has no PTY", id)
	}
	return pty.Setsize(ptyFile, &pty.Winsize{
		Cols: uint16(cols),
		Rows: uint16(rows),
	})
}

// Input implements TerminalSessionService.
// Writes raw bytes directly to the session's PTY file descriptor.
func (s *InProcessService) Input(_ context.Context, id string, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	sess, err := s.manager.Get(id)
	if err != nil {
		return err
	}
	sess.mu.Lock()
	ptyFile := sess.PTY
	sess.mu.Unlock()
	if ptyFile == nil {
		return fmt.Errorf("session %s has no PTY", id)
	}
	_, writeErr := ptyFile.Write(data)
	if writeErr != nil {
		return fmt.Errorf("pty write: %w", writeErr)
	}
	sess.mu.Lock()
	sess.LastActive = time.Now()
	sess.mu.Unlock()
	return nil
}

// PasteUpload implements TerminalSessionService.
// Saves content to a temp directory under the resolved session cwd and returns the
// absolute path of the saved file. The logic mirrors clipboard_paste.go but operates
// on io.Reader rather than gin.Context so that it can be called without an HTTP layer.
func (s *InProcessService) PasteUpload(_ context.Context, id string, filename string, content io.Reader, sessionCWD string) (string, error) {
	start := time.Now()
	sess, err := s.manager.Get(id)
	if err != nil {
		terminalClipboardUploadErrors.Inc()
		return "", err
	}

	// cwd resolution mirrors Server.workbenchCWD's fallback ORDER so this in-proc (Wails
	// desktop) paste lands in the SAME directory the HTTP paste-upload would: an explicit
	// override wins, else the shell's LIVE /proc cwd (which follows `cd`), else the static
	// creation cwd as a last resort. Batch-1 fixed the HTTP path; this closes the same
	// "falls back to the frozen launch dir" bug on the in-proc bypass.
	// NOTE: unlike workbenchCWD, InProcessService holds only a SessionManager (no Server /
	// tmuxProvider), so it can't consult the tmux active-pane authority — liveShellCWD is
	// the closest equivalent it can reach. For a non-tmux shell the shell IS the pane, so
	// /proc is exact; for a tmux session it's tmux's own cwd, still strictly better than the
	// frozen WorkingDir it replaces.
	cwd := sessionCWD
	if cwd == "" {
		if live := liveShellCWD(sess); live != "" {
			cwd = live
		} else {
			cwd = sess.WorkingDir()
		}
	}
	if cwd == "" {
		terminalClipboardUploadErrors.Inc()
		return "", fmt.Errorf("session %s has no working directory", id)
	}

	// Derive sub-directory: images go to tmp/clip/{date}, others to tmp/files/{date}.
	now := time.Now()
	subDir := "clip"
	ext := strings.ToLower(filepath.Ext(filename))
	if !isImageExt(ext) {
		subDir = "files"
	}
	hourDir := filepath.Join(cwd, "tmp", subDir, now.Format("01-02-15"))
	if err := os.MkdirAll(hourDir, 0700); err != nil {
		terminalClipboardUploadErrors.Inc()
		return "", fmt.Errorf("cannot create clipboard dir: %w", err)
	}

	// Read content for hash dedup.
	data, err := io.ReadAll(io.LimitReader(content, ClipboardMaxUploadSize))
	if err != nil {
		terminalClipboardUploadErrors.Inc()
		return "", fmt.Errorf("read content: %w", err)
	}

	hash := sha256.Sum256(data)
	hashHex := hex.EncodeToString(hash[:8])

	// Dedup check.
	if existing := findDuplicateClipboard(hourDir, hashHex, data); existing != "" {
		terminalClipboardUploadsTotal.Inc()
		terminalClipboardUploadBytes.Add(uint64(len(data)))
		terminalClipboardUploadDuration.Observe(time.Since(start).Seconds())
		return existing, nil
	}

	// Build filename. Preserve the original name for any real copied file (image or
	// not); only a nameless bitmap falls through to the synthetic hash name. Mirrors
	// handleClipboardPasteUpload in clipboard_paste.go (shared isGenericClipboardName).
	var saveName string
	origName := sanitizeClipboardFilename(filename)
	switch {
	case !isGenericClipboardName(origName):
		saveName = uniqueClipboardFilename(hourDir, origName, hashHex)
	case isImageExt(ext):
		seq := nextClipboardSeq(id)
		if ext == "" {
			ext = ".bin"
		}
		saveName = fmt.Sprintf("%s%03d-%s%s", now.Format("1504"), seq, hashHex, ext)
	default:
		if ext == "" {
			ext = ".bin"
		}
		saveName = uniqueClipboardFilename(hourDir, "upload"+ext, hashHex)
	}

	savePath := filepath.Join(hourDir, saveName)
	tmpPath := savePath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		terminalClipboardUploadErrors.Inc()
		return "", fmt.Errorf("write temp file: %w", err)
	}
	if err := os.Rename(tmpPath, savePath); err != nil {
		_ = os.Remove(tmpPath)
		terminalClipboardUploadErrors.Inc()
		return "", fmt.Errorf("rename: %w", err)
	}

	terminalClipboardUploadsTotal.Inc()
	terminalClipboardUploadBytes.Add(uint64(len(data)))
	terminalClipboardUploadDuration.Observe(time.Since(start).Seconds())
	return savePath, nil
}

// TailOutput implements TerminalSessionService.
func (s *InProcessService) TailOutput(_ context.Context, id string, lines int) ([]string, error) {
	sess, err := s.manager.Get(id)
	if err != nil {
		return nil, err
	}
	return sess.TailOutput(lines), nil
}

// Close implements TerminalSessionService.
func (s *InProcessService) Close() error {
	s.manager.DestroyAll()
	return nil
}

// sessionToInfo converts an internal Session to a SessionInfo DTO.
// Must be called without holding sess.mu (it acquires the lock itself).
func sessionToInfo(sess *Session) SessionInfo {
	sess.mu.Lock()
	status := sess.Status
	lastActive := sess.LastActive
	exitCode := sess.exitCode
	tmuxDetected := sess.TmuxDetected
	sess.mu.Unlock()

	return SessionInfo{
		ID:           sess.ID,
		Name:         sess.Name,
		Title:        sess.Title,
		Engine:       sess.Engine,
		CWD:          sess.CWD,
		Status:       status,
		CreatedAt:    sess.CreatedAt.Format(time.RFC3339),
		LastActive:   lastActive.Format(time.RFC3339),
		ShellPID:     sess.ShellPID(),
		TmuxDetected: tmuxDetected,
		ExitCode:     exitCode,
	}
}

// isImageExt returns true for common image file extensions.
func isImageExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg":
		return true
	}
	return false
}
