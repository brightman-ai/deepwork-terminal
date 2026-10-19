package terminal

import (
	"archive/zip"
	"context"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMarkdownDocxEmbedsSourceRelativeImage(t *testing.T) {
	if _, err := exec.LookPath(markdownConvertBin); err != nil {
		t.Skip("pandoc is required for the real conversion oracle")
	}
	sourceDir := filepath.Join(t.TempDir(), "中文 source")
	require.NoError(t, os.MkdirAll(filepath.Join(sourceDir, "assets"), 0o755))
	picture, err := os.Create(filepath.Join(sourceDir, "assets", "chart one.png"))
	require.NoError(t, err)
	require.NoError(t, png.Encode(picture, image.NewRGBA(image.Rect(0, 0, 8, 8))))
	require.NoError(t, picture.Close())
	src := filepath.Join(sourceDir, "图文.md")
	require.NoError(t, os.WriteFile(src, []byte("# 图文验证\n\n![中文图](<assets/chart one.png>)\n"), 0o644))
	info, err := os.Stat(src)
	require.NoError(t, err)
	s := &Server{config: Config{DataDir: t.TempDir()}}
	out, err := s.convertMarkdownToDocx(context.Background(), src, info)
	require.NoError(t, err)
	z, err := zip.OpenReader(filepath.Join(out, "main.docx"))
	require.NoError(t, err)
	defer z.Close()
	hasImage := false
	for _, f := range z.File {
		if strings.HasPrefix(f.Name, "word/media/") && f.UncompressedSize64 > 0 {
			hasImage = true
		}
	}
	require.True(t, hasImage, "a successful illustrated DOCX must contain the image bytes")
}

func TestMarkdownDocxReportsMissingImage(t *testing.T) {
	if _, err := exec.LookPath(markdownConvertBin); err != nil {
		t.Skip("pandoc is required for the real conversion oracle")
	}
	src := filepath.Join(t.TempDir(), "missing.md")
	require.NoError(t, os.WriteFile(src, []byte("![missing](absent.png)\n"), 0o644))
	info, err := os.Stat(src)
	require.NoError(t, err)
	s := &Server{config: Config{DataDir: t.TempDir()}}
	_, err = s.convertMarkdownToDocx(context.Background(), src, info)
	require.Error(t, err, "must not silently deliver a DOCX with missing illustrations")
}
