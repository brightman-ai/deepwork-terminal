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
)

// Tunnel manages a cloudflared quick-tunnel process.
type Tunnel struct {
	mu        sync.Mutex
	cmd       *exec.Cmd
	publicURL string
	running   bool
	binPath   string
}

// NewTunnel creates a tunnel manager. dataDir is where cloudflared will be cached.
func NewTunnel(dataDir string) *Tunnel {
	binPath := filepath.Join(dataDir, "bin", "cloudflared")
	if runtime.GOOS == "windows" {
		binPath += ".exe"
	}
	return &Tunnel{binPath: binPath}
}

// Start starts the tunnel, auto-downloading cloudflared if needed.
// Returns the public URL.
func (t *Tunnel) Start(ctx context.Context, localAddr string) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.running {
		return t.publicURL, nil
	}

	// Ensure cloudflared binary exists.
	if err := t.ensureBinary(ctx); err != nil {
		return "", fmt.Errorf("cloudflared setup: %w", err)
	}

	// Start tunnel.
	t.cmd = exec.CommandContext(ctx, t.binPath, "tunnel", "--url", localAddr)
	stderr, err := t.cmd.StderrPipe()
	if err != nil {
		return "", err
	}
	if err := t.cmd.Start(); err != nil {
		return "", fmt.Errorf("start cloudflared: %w", err)
	}

	// Parse URL from stderr output.
	url, err := parseTunnelURL(stderr)
	if err != nil {
		t.cmd.Process.Kill() //nolint:errcheck
		return "", fmt.Errorf("parse tunnel URL: %w", err)
	}

	t.publicURL = url
	t.running = true
	return url, nil
}

// Stop stops the tunnel.
func (t *Tunnel) Stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cmd != nil && t.cmd.Process != nil {
		t.cmd.Process.Kill() //nolint:errcheck
		t.cmd.Wait()         //nolint:errcheck
	}
	t.running = false
	t.publicURL = ""
}

// PublicURL returns the current tunnel URL.
func (t *Tunnel) PublicURL() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.publicURL
}

// IsRunning returns whether the tunnel is active.
func (t *Tunnel) IsRunning() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.running
}

// ensureBinary downloads cloudflared if not present.
func (t *Tunnel) ensureBinary(ctx context.Context) error {
	if _, err := os.Stat(t.binPath); err == nil {
		return nil // already exists
	}

	downloadURL := cloudflaredDownloadURL()
	if downloadURL == "" {
		return fmt.Errorf("unsupported platform: %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	os.MkdirAll(filepath.Dir(t.binPath), 0755) //nolint:errcheck

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
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

	if strings.HasSuffix(downloadURL, ".tgz") {
		// macOS: extract the binary from the tgz archive.
		return extractTgz(resp.Body, t.binPath)
	}

	// Linux/Windows: direct binary download.
	f, err := os.Create(t.binPath)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(t.binPath) //nolint:errcheck
		return err
	}
	f.Close()
	return os.Chmod(t.binPath, 0755)
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

// parseTunnelURL reads cloudflared stderr until it prints the tunnel URL.
var tunnelURLRegex = regexp.MustCompile(`https://[a-zA-Z0-9-]+\.trycloudflare\.com`)

func parseTunnelURL(r io.Reader) (string, error) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if match := tunnelURLRegex.FindString(line); match != "" {
			return match, nil
		}
		// Detect fatal error conditions early.
		if strings.Contains(line, "failed") && strings.Contains(line, "tunnel") {
			return "", fmt.Errorf("cloudflared error: %s", line)
		}
	}
	return "", fmt.Errorf("cloudflared exited without producing a URL")
}

// extractTgz extracts the cloudflared binary from a .tgz archive.
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
			f, err := os.Create(targetPath)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				os.Remove(targetPath) //nolint:errcheck
				return err
			}
			f.Close()
			return os.Chmod(targetPath, 0755)
		}
	}
	return fmt.Errorf("cloudflared binary not found in archive")
}
