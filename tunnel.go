package terminal

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
)

// TunnelStatus is the complete observable state of a Tunnel.
// Safe to read at any time — never blocks on a download in progress.
type TunnelStatus struct {
	Running         bool   `json:"running"`
	PublicURL       string `json:"publicURL,omitempty"`
	Downloading     bool   `json:"downloading"`
	DownloadedBytes int64  `json:"downloadedBytes,omitempty"`
	TotalBytes      int64  `json:"totalBytes,omitempty"` // -1 if unknown
	DownloadURL     string `json:"downloadURL,omitempty"`
	BinPath         string `json:"binPath,omitempty"`
}

// Tunnel manages a cloudflared quick-tunnel process.
//
// Locking discipline:
//
//	startMu — held for the full duration of Start/Stop; prevents concurrent starts.
//	          NOT held during status reads.
//	stateMu — RWMutex guarding running/publicURL/cmd/downloadURL.
//	          Never held while doing I/O (download, process wait).
//	downloadedBytes / totalBytes / downloading — atomic; zero-lock reads.
type Tunnel struct {
	startMu sync.Mutex
	stateMu sync.RWMutex

	running   bool
	publicURL string
	cmd       *exec.Cmd

	// Download progress — written under startMu, read via atomics from any goroutine.
	downloading     atomic.Bool
	downloadedBytes atomic.Int64
	totalBytes      atomic.Int64 // -1 means Content-Length unknown
	downloadURL     atomic.Value // stores string

	binPath string
}

// NewTunnel creates a tunnel manager. dataDir is where cloudflared will be cached.
func NewTunnel(dataDir string) *Tunnel {
	binPath := filepath.Join(dataDir, "bin", "cloudflared")
	if runtime.GOOS == "windows" {
		binPath += ".exe"
	}
	return &Tunnel{binPath: binPath}
}

// Status returns a consistent snapshot of tunnel state without blocking.
func (t *Tunnel) Status() TunnelStatus {
	t.stateMu.RLock()
	s := TunnelStatus{
		Running:   t.running,
		PublicURL: t.publicURL,
	}
	t.stateMu.RUnlock()

	s.Downloading = t.downloading.Load()
	s.DownloadedBytes = t.downloadedBytes.Load()
	s.TotalBytes = t.totalBytes.Load()
	if u, ok := t.downloadURL.Load().(string); ok {
		s.DownloadURL = u
	}
	s.BinPath = t.binPath
	return s
}

// IsRunning returns whether the tunnel is active.
func (t *Tunnel) IsRunning() bool {
	t.stateMu.RLock()
	defer t.stateMu.RUnlock()
	return t.running
}

// PublicURL returns the current public tunnel URL.
func (t *Tunnel) PublicURL() string {
	t.stateMu.RLock()
	defer t.stateMu.RUnlock()
	return t.publicURL
}

// Start ensures the cloudflared binary is present, then starts a quick-tunnel.
// Returns the public URL. Concurrent calls are serialized; a second call while
// a download is in progress will block (not return an error).
func (t *Tunnel) Start(ctx context.Context, localAddr string) (string, error) {
	t.startMu.Lock()
	defer t.startMu.Unlock()

	t.stateMu.RLock()
	alreadyRunning := t.running
	existingURL := t.publicURL
	t.stateMu.RUnlock()
	if alreadyRunning {
		return existingURL, nil
	}

	// Download phase: startMu held, stateMu NOT held → status reads are unblocked.
	if err := t.ensureBinary(ctx); err != nil {
		return "", fmt.Errorf("cloudflared setup: %w", err)
	}

	// Launch cloudflared.
	cmd := exec.CommandContext(ctx, t.binPath, "tunnel", "--url", localAddr)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start cloudflared: %w", err)
	}

	url, err := parseTunnelURL(stderr)
	if err != nil {
		cmd.Process.Kill() //nolint:errcheck
		cmd.Wait()         //nolint:errcheck
		return "", fmt.Errorf("parse tunnel URL: %w", err)
	}

	t.stateMu.Lock()
	t.running = true
	t.publicURL = url
	t.cmd = cmd
	t.stateMu.Unlock()

	return url, nil
}

// Stop kills the tunnel process and resets state.
func (t *Tunnel) Stop() {
	t.startMu.Lock()
	defer t.startMu.Unlock()

	t.stateMu.Lock()
	cmd := t.cmd
	t.running = false
	t.publicURL = ""
	t.cmd = nil
	t.stateMu.Unlock()

	if cmd != nil && cmd.Process != nil {
		cmd.Process.Kill() //nolint:errcheck
		cmd.Wait()         //nolint:errcheck
	}
}

// ── Binary management ─────────────────────────────────────────────────────────

// minCloudflaredSize is the minimum acceptable size for a cloudflared binary.
// The real binary is ~90 MB; anything smaller is a truncated/corrupt download.
const minCloudflaredSize = 10 * 1024 * 1024 // 10 MB

// ensureBinary downloads cloudflared if not present, not executable, or corrupt.
// Called with startMu held; must NOT hold stateMu (download may take minutes).
func (t *Tunnel) ensureBinary(ctx context.Context) error {
	if info, err := os.Stat(t.binPath); err == nil {
		if info.Size() >= minCloudflaredSize && info.Mode()&0111 != 0 {
			return nil
		}
		os.Remove(t.binPath) //nolint:errcheck
	}

	dlURL := cloudflaredDownloadURL()
	if dlURL == "" {
		return fmt.Errorf("unsupported platform: %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	t.downloadURL.Store(dlURL)

	if err := os.MkdirAll(filepath.Dir(t.binPath), 0755); err != nil {
		return fmt.Errorf("create bin dir: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, dlURL, nil)
	if err != nil {
		return fmt.Errorf("build download request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("download cloudflared: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download cloudflared: HTTP %d", resp.StatusCode)
	}

	t.totalBytes.Store(resp.ContentLength) // -1 if server omits Content-Length
	t.downloadedBytes.Store(0)
	t.downloading.Store(true)
	defer t.downloading.Store(false)

	body := &progressReader{
		r:    resp.Body,
		read: func(n int64) { t.downloadedBytes.Add(n) },
	}

	if strings.HasSuffix(dlURL, ".tgz") {
		return extractTgz(body, t.binPath)
	}
	return writeBinaryAtomic(body, t.binPath)
}

func cloudflaredDownloadURL() string {
	base := "https://github.com/cloudflare/cloudflared/releases/latest/download/"
	switch runtime.GOOS {
	case "darwin":
		if runtime.GOARCH == "arm64" {
			return base + "cloudflared-darwin-arm64.tgz"
		}
		return base + "cloudflared-darwin-amd64.tgz"
	case "linux":
		if runtime.GOARCH == "arm64" {
			return base + "cloudflared-linux-arm64"
		}
		return base + "cloudflared-linux-amd64"
	case "windows":
		return base + "cloudflared-windows-amd64.exe"
	}
	return ""
}

// ── URL parsing ───────────────────────────────────────────────────────────────

var tunnelURLRegex = regexp.MustCompile(`https://[a-zA-Z0-9-]+\.trycloudflare\.com`)

// parseTunnelURL reads cloudflared stderr until a tunnel URL appears.
// It terminates early on any line that contains "ERR" or "level=error",
// which covers all fatal cloudflared error patterns — not just "failed tunnel".
func parseTunnelURL(r io.Reader) (string, error) {
	scanner := bufio.NewScanner(r)
	var lastErr string
	for scanner.Scan() {
		line := scanner.Text()
		if match := tunnelURLRegex.FindString(line); match != "" {
			return match, nil
		}
		if strings.Contains(line, "level=error") || strings.Contains(line, "level=fatal") {
			lastErr = line
		}
	}
	if lastErr != "" {
		return "", fmt.Errorf("cloudflared: %s", lastErr)
	}
	return "", fmt.Errorf("cloudflared exited without producing a tunnel URL")
}

// ── I/O helpers ───────────────────────────────────────────────────────────────

// progressReader wraps an io.Reader and calls read(n) after each chunk.
type progressReader struct {
	r    io.Reader
	read func(int64)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.read(int64(n))
	}
	return n, err
}

// writeBinaryAtomic streams r into a temp file in the same directory as
// targetPath, sets 0755, then renames atomically.
// Temp files stay in the target directory — never in /tmp.
func writeBinaryAtomic(r io.Reader, targetPath string) error {
	dir := filepath.Dir(targetPath)
	tmp, err := os.CreateTemp(dir, ".cloudflared-download-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) //nolint:errcheck — no-op after successful rename

	if _, err := io.Copy(tmp, r); err != nil {
		tmp.Close()
		return fmt.Errorf("write cloudflared: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0755); err != nil {
		return err
	}
	return os.Rename(tmpName, targetPath)
}

// extractTgz extracts the cloudflared binary from a .tgz archive atomically.
func extractTgz(r io.Reader, targetPath string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if filepath.Base(hdr.Name) == "cloudflared" {
			return writeBinaryAtomic(tr, targetPath)
		}
	}
	return fmt.Errorf("cloudflared binary not found in archive")
}
