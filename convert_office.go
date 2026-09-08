package terminal

// convert_office.go — 服务端 office 转换（libreoffice headless），files-panel 统一预览的承重件。
//
// ── 为什么在服务端 ────────────────────────────────────────────────────────────────────────────
// 客户端没有可用的 pptx / xls / doc 渲染器，而 libreoffice 转换保真且本机已装。产物喂给前端
// 既有的渲染器，一套渲染两端（PC/手机）复用，不为每种格式再引一个前端库。
//
// ── 目标格式按"族"归一，不是各写一种渲染 ──────────────────────────────────────────────────────
//   pptx/ppt → pdf   （复用 PdfPreview：连续滚页 + 文本层）
//   xlsx/xls → html  （2026-09-08 实测：4.3MB 表转出 410KB html，3 个 <table> = 3 个工作表，
//                     colspan/rowspan 保住，工作表名在 <A NAME="tableN"><h1>工作表 N: <em>名</em></h1></A>，
//                     且产物里 <script>/on* 事件为 0。文本是真 HTML —— 天然可选中复制，
//                     这正是"预览要支持内容复制"的要求，转 pdf 反而做不到。)
//   doc      → docx  （归一到新格式后走客户端 DocxPreview —— 与 docx 观感逐字一致，
//                     而不是让 .doc 长得跟 .docx 不一样。)
//
// ── 缓存：目录形态 ────────────────────────────────────────────────────────────────────────────
// <DataDir>/convert-cache/<sha256(absPath|mtimeNanos|size)[:16]>/
//     main.<target>   ← 规范名（源文件名可能含空格/中文/引号，规范化掉省一路转义）
//     <其它产物>       ← html 转换会额外吐出内嵌图片（xlsx 里的图表截图），名字由 libreoffice 定
//     .done           ← 最后写；没有它就当没转过（防半截产物被当命中）
// mtime+size 进键 → 源文件被改自动失效。整目录先在 tmp- 前缀下转好再 rename 就位（原子）。

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
	officeConvertMaxBytes  = 50 << 20 // 50MB 源文件上限——更大的文件转换分钟级，拒绝比挂死诚实
	officeConvertTimeout   = 60 * time.Second
	officeConvertBin       = "libreoffice"
	officeConvertCacheName = "convert-cache"
	convertDoneMarker      = ".done"
)

var convertMu sync.Mutex

// convertTargetFor maps a lowercased extension (with dot) to the format we convert it to,
// or "" when the file is not a conversion candidate. This is the backend SSOT for
// "which files go through libreoffice"; the frontend's previewFormats.ts carries the
// matching table for routing/labelling, and the two must agree.
func convertTargetFor(ext string) string {
	switch ext {
	case ".pptx", ".ppt":
		return "pdf"
	case ".xlsx", ".xls":
		return "html"
	case ".doc":
		return "docx"
	}
	return ""
}

// convertContentType is the Content-Type we serve each conversion product with.
func convertContentType(target string) string {
	switch target {
	case "pdf":
		return "application/pdf"
	case "html":
		return "text/html; charset=utf-8"
	case "docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	}
	return "application/octet-stream"
}

// convertOffice converts src to the requested target via headless libreoffice, cached by
// (path, mtime, size). Returns the cache DIRECTORY — the product is `main.<target>` inside
// it, and any sibling files are assets the product references (see the header).
func (s *Server) convertOffice(ctx context.Context, src string, info os.FileInfo, target string) (string, error) {
	cacheDir := filepath.Join(s.config.DataDir, officeConvertCacheName)
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", fmt.Errorf("cache dir: %w", err)
	}
	h := sha256.Sum256([]byte(src + "|" + strconv.FormatInt(info.ModTime().UnixNano(), 10) + "|" +
		strconv.FormatInt(info.Size(), 10) + "|" + target))
	outDir := filepath.Join(cacheDir, hex.EncodeToString(h[:16]))

	if convertCacheHit(outDir) {
		return outDir, nil
	}

	// 串行：libreoffice 是重进程，并发转换既慢又抢内存；调用频率=用户点开一个文件，天然低频。
	convertMu.Lock()
	defer convertMu.Unlock()
	if convertCacheHit(outDir) { // double-check：排队等锁期间别人可能已转完
		return outDir, nil
	}

	cctx, cancel := context.WithTimeout(ctx, officeConvertTimeout)
	defer cancel()
	// 独立 profile：默认 profile 有并发锁，第二个 headless 实例直接失败。UserInstallation 指到
	// 缓存目录下，多实例互不干扰（也远离用户真实 LO 配置）。
	profile := filepath.ToSlash(filepath.Join(cacheDir, "lo-profile"))
	tmpDir, err := os.MkdirTemp(cacheDir, "tmp-")
	if err != nil {
		return "", fmt.Errorf("tmp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir) // rename 成功后这里已空

	cmd := exec.CommandContext(cctx, officeConvertBin,
		"--headless", "--norestore", "--nolockcheck",
		"-env:UserInstallation=file://"+profile,
		"--convert-to", target, "--outdir", tmpDir, src)
	out, cerr := cmd.CombinedOutput()
	if cerr != nil {
		slog.Warn("office convert failed", "src", filepath.Base(src), "target", target,
			"err", cerr, "out", strings.TrimSpace(string(out)))
		return "", fmt.Errorf("libreoffice: %w", cerr)
	}
	produced := filepath.Join(tmpDir, strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))+"."+target)
	if _, serr := os.Stat(produced); serr != nil {
		return "", fmt.Errorf("libreoffice produced nothing for %s", target)
	}
	if err := os.Rename(produced, filepath.Join(tmpDir, "main."+target)); err != nil {
		return "", fmt.Errorf("normalize product name: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, convertDoneMarker), nil, 0o644); err != nil {
		return "", fmt.Errorf("done marker: %w", err)
	}
	if err := os.Rename(tmpDir, outDir); err != nil {
		// 并发窗口：另一个请求刚好把同一个 key 就位了 —— 它的产物同样有效。
		if convertCacheHit(outDir) {
			return outDir, nil
		}
		return "", fmt.Errorf("publish product: %w", err)
	}
	return outDir, nil
}

// convertCacheHit reports whether a cache directory holds a COMPLETE conversion. The marker
// is written last, so a crashed/timed-out conversion never reads back as a hit.
func convertCacheHit(dir string) bool {
	st, err := os.Stat(filepath.Join(dir, convertDoneMarker))
	return err == nil && !st.IsDir()
}
