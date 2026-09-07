package terminal

// convert_pptx.go — pptx→pdf 服务端转换（2026-09-07，files-panel「pptx 预览」）。
//
// 为什么在服务端：客户端没有可用的 pptx 渲染器（pptx-preview 类库质量差/无维护），
// 而 libreoffice headless 转换保真（版式/图/字体）且本机已装。产物喂给前端既有的
// PdfPreview，一套渲染两端（PC/手机）复用。
//
// 缓存：<DataDir>/convert-cache/<sha256(absPath|mtimeNanos|size)>.pdf —— mtime+size 进键，
// 源文件被改自动失效；产物带 no-cache 头，浏览器不钉旧版。libreoffice 的并发 profile
// 锁用独立 UserInstallation 目录绕开；转换全程互斥（转换是重操作，串行即够）。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	pptxConvertMaxBytes  = 50 << 20 // 50MB 源文件上限——更大的 pptx 转换分钟级，拒绝比挂死诚实
	pptxConvertTimeout   = 30 * time.Second
	pptxConvertBin       = "libreoffice"
	pptxConvertCacheName = "convert-cache"
)

var convertMu sync.Mutex

func isPptxExt(ext string) bool {
	return ext == ".pptx" || ext == ".ppt"
}

// convertToPDF converts an office file to PDF via headless libreoffice, cached by
// (path, mtime, size). Returns the cached PDF's path.
func (s *Server) convertToPDF(ctx context.Context, src string, info os.FileInfo) (string, error) {
	cacheDir := filepath.Join(s.config.DataDir, pptxConvertCacheName)
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", fmt.Errorf("cache dir: %w", err)
	}
	h := sha256.Sum256([]byte(src + "|" + strconv.FormatInt(info.ModTime().UnixNano(), 10) + "|" + strconv.FormatInt(info.Size(), 10)))
	cachePath := filepath.Join(cacheDir, hex.EncodeToString(h[:16])+".pdf")

	if st, err := os.Stat(cachePath); err == nil && st.Size() > 0 {
		return cachePath, nil // 命中：mtime/size 未变
	}

	// 串行：libreoffice 是重进程，并发转换既慢又抢内存；调用频率=用户点开 pptx，天然低频。
	convertMu.Lock()
	defer convertMu.Unlock()
	// double-check：排队等锁期间别人可能已转完
	if st, err := os.Stat(cachePath); err == nil && st.Size() > 0 {
		return cachePath, nil
	}

	cctx, cancel := context.WithTimeout(ctx, pptxConvertTimeout)
	defer cancel()
	// 独立 profile：默认 profile 有并发锁，第二个 headless 实例直接失败。
	// UserInstallation 指到缓存目录下，多实例互不干扰（也远离用户真实 LO 配置）。
	profile := filepath.ToSlash(filepath.Join(cacheDir, "lo-profile"))
	outDir := filepath.Join(cacheDir, "tmp-out")
	_ = os.MkdirAll(outDir, 0o755)
	defer os.RemoveAll(outDir)
	cmd := exec.CommandContext(cctx, pptxConvertBin,
		"--headless", "--norestore", "--nolockcheck",
		"-env:UserInstallation=file://"+profile,
		"--convert-to", "pdf", "--outdir", outDir, src)
	out, err := cmd.CombinedOutput()
	if err != nil {
		slog.Warn("pptx convert failed", "src", filepath.Base(src), "err", err, "out", strings.TrimSpace(string(out)))
		return "", fmt.Errorf("libreoffice: %w", err)
	}
	produced := filepath.Join(outDir, strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))+".pdf")
	if err := os.Rename(produced, cachePath); err != nil {
		return "", fmt.Errorf("move product: %w", err)
	}
	return cachePath, nil
}
