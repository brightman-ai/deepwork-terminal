package terminal

// archive_preview.go — zip 家族的只读预览（2026-09-08，files-panel 统一预览）。
//
// 覆盖两种文件，同一套机制：
//   .zip    → 列条目清单（路径/大小/压缩率）。**不解压**：解压到临时目录会引出生命周期、清理、
//             zip 炸弹三条新问题，而"这里面装了什么"这一个问题，清单就答完了。
//   .xmind  → 它本身就是个 zip：`content.json`（大纲树，可复制）+ `Thumbnails/thumbnail.png`
//             （XMind 自己渲染好的导图，可放大）。所以思维导图不需要我们画布局 —— 取两个条目即可。
//
// docx/xlsx/pptx 同样是 zip，但**刻意不走这条路**：它们有正经渲染器，把 OOXML 的内脏摊开给人看
// 只会让"预览"这个词失去意义。

import (
	"archive/zip"
	"errors"
	"io"
	"path"
	"strings"
)

const (
	zipMaxEntries    = 2000    // 清单上限：再多就不是"看一眼"了，截断并如实告知
	zipEntryMaxBytes = 8 << 20 // 单条目取出上限（xmind 的 content.json 实测 42KB、缩略图 461KB）
)

// isZipFamilyExt reports whether we open this extension as a zip archive.
func isZipFamilyExt(ext string) bool {
	return ext == ".zip" || ext == ".xmind"
}

type zipEntry struct {
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	Compressed int64  `json:"compressed"`
	IsDir      bool   `json:"isDir"`
}

// listZipEntries reads the central directory only — no decompression, so a zip bomb costs
// nothing here. Returns the entries and whether the listing was truncated.
func listZipEntries(src string) ([]zipEntry, bool, error) {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return nil, false, err
	}
	defer zr.Close()
	out := make([]zipEntry, 0, len(zr.File))
	truncated := false
	for _, f := range zr.File {
		if len(out) >= zipMaxEntries {
			truncated = true
			break
		}
		out = append(out, zipEntry{
			Name:       f.Name,
			Size:       int64(f.UncompressedSize64),
			Compressed: int64(f.CompressedSize64),
			IsDir:      f.FileInfo().IsDir(),
		})
	}
	return out, truncated, nil
}

// readZipEntry returns one entry's bytes, capped. The name must match an entry EXACTLY —
// it is looked up in the central directory, never joined onto a filesystem path, so the
// usual archive path-traversal class does not arise.
func readZipEntry(src, name string) ([]byte, error) {
	if name == "" || strings.Contains(name, "..") {
		return nil, errors.New("bad entry name")
	}
	zr, err := zip.OpenReader(src)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name != name || f.FileInfo().IsDir() {
			continue
		}
		if f.UncompressedSize64 > zipEntryMaxBytes {
			return nil, errors.New("entry too large to preview")
		}
		rc, oerr := f.Open()
		if oerr != nil {
			return nil, oerr
		}
		defer rc.Close()
		// LimitReader 是第二道：声明的 UncompressedSize64 可以撒谎，真正的护栏是读取上限。
		return io.ReadAll(io.LimitReader(rc, zipEntryMaxBytes+1))
	}
	return nil, errors.New("entry not found")
}

// zipEntryContentType maps an entry name to the Content-Type we serve it with. Only the two
// shapes an xmind preview needs (json text, raster image) — anything else is a download,
// not a preview.
func zipEntryContentType(name string) string {
	switch ext := strings.ToLower(path.Ext(name)); ext {
	case ".json", ".xml", ".txt", ".md":
		return "text/plain; charset=utf-8"
	default:
		return imageContentType(ext)
	}
}

// audioContentType maps a lowercased extension (with dot) to its audio MIME type, or "".
// Serving audio INLINE (rather than as the {binary:true} sentinel) is what lets the drawer
// hand the bytes to a native <audio> element — the meeting recordings in a work repo are
// artifacts you want to play, not download.
func audioContentType(ext string) string {
	switch ext {
	case ".m4a", ".mp4a", ".aac":
		return "audio/mp4"
	case ".mp3":
		return "audio/mpeg"
	case ".wav":
		return "audio/wav"
	case ".ogg", ".oga":
		return "audio/ogg"
	case ".flac":
		return "audio/flac"
	case ".opus":
		return "audio/opus"
	default:
		return ""
	}
}
