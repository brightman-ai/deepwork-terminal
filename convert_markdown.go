package terminal

// Markdown → DOCX conversion. This is intentionally separate from the
// LibreOffice office-family converter: pandoc understands GFM/Markdown as
// source, while LibreOffice is the SSOT for legacy office inputs.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	markdownConvertTimeout = 45 * time.Second
	markdownConvertBin     = "pandoc"
)

func isMarkdownExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".md", ".markdown", ".mdx":
		return true
	default:
		return false
	}
}

// convertMarkdownToDocx returns the same cache-directory shape as the office
// converter, so the raw endpoint can serve main.docx and the browser can fetch
// bytes without writing a generated file into the user's working tree.
func (s *Server) convertMarkdownToDocx(ctx context.Context, src string, info os.FileInfo) (string, error) {
	cacheDir := filepath.Join(s.config.DataDir, officeConvertCacheName)
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", fmt.Errorf("cache dir: %w", err)
	}
	h := sha256.Sum256([]byte(src + "|" + strconv.FormatInt(info.ModTime().UnixNano(), 10) + "|" + strconv.FormatInt(info.Size(), 10) + "|docx|gfm-resources-v2"))
	outDir := filepath.Join(cacheDir, hex.EncodeToString(h[:16]))
	if convertCacheHit(outDir) {
		return outDir, nil
	}

	convertMu.Lock()
	defer convertMu.Unlock()
	if convertCacheHit(outDir) {
		return outDir, nil
	}
	if _, err := exec.LookPath(markdownConvertBin); err != nil {
		return "", fmt.Errorf("pandoc is not installed")
	}
	cctx, cancel := context.WithTimeout(ctx, markdownConvertTimeout)
	defer cancel()
	tmpDir, err := os.MkdirTemp(cacheDir, "tmp-md-")
	if err != nil {
		return "", fmt.Errorf("tmp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	out := filepath.Join(tmpDir, "main.docx")
	cmd := exec.CommandContext(cctx, markdownConvertBin,
		"--from=gfm", "--to=docx", "--fail-if-warnings", "--resource-path="+filepath.Dir(src), "--output="+out, src)
	// Markdown images are relative to the source, not the terminal server's launch directory.
	// Failing on missing resources prevents a successful-looking DOCX that silently lost images.
	cmd.Dir = filepath.Dir(src)
	if output, runErr := cmd.CombinedOutput(); runErr != nil {
		return "", fmt.Errorf("pandoc: %w (%s)", runErr, strings.TrimSpace(string(output)))
	}
	if st, statErr := os.Stat(out); statErr != nil || st.IsDir() || st.Size() == 0 {
		return "", fmt.Errorf("pandoc produced no docx")
	}
	if err := os.WriteFile(filepath.Join(tmpDir, convertDoneMarker), nil, 0o644); err != nil {
		return "", fmt.Errorf("done marker: %w", err)
	}
	if err := os.Rename(tmpDir, outDir); err != nil {
		if convertCacheHit(outDir) {
			return outDir, nil
		}
		return "", fmt.Errorf("publish product: %w", err)
	}
	return outDir, nil
}
