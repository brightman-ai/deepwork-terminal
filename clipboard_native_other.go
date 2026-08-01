//go:build !darwin

package terminal

import (
	"bytes"
	"context"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
)

// readClipboardFilePaths returns the files on the system clipboard, or nothing at all if
// this platform has no way to answer.
//
// "Nothing" is a first-class answer, not a degraded one: the paste resolver's next step is
// to upload the bytes the browser already holds, which is exactly what every non-macOS
// deployment does today. So every failure below — no helper installed, helper exits
// non-zero because the clipboard holds no URI list, unknown OS — returns (nil, nil). An
// error would be reported to the user as a broken probe when the truthful statement is
// "there are no file paths here".
//
// ○ UNVERIFIED: the Linux branch is written from the wl-paste/xclip contracts and has not
// been run on a Linux desktop. Its blast radius is bounded by the paragraph above — the
// worst outcome is the fallback that is already in place.
func readClipboardFilePaths(ctx context.Context) ([]string, error) {
	if runtime.GOOS != "linux" {
		return nil, nil
	}
	// text/uri-list is the freedesktop clipboard target a file manager publishes when the
	// user copies files; asking for it directly is what distinguishes "files were copied"
	// from "some text that happens to look like a path was copied". xsel is absent from the
	// list on purpose: it cannot select a target, so it can only ever return plain text.
	for _, candidate := range [][]string{
		{"wl-paste", "--no-newline", "--type", "text/uri-list"},
		{"xclip", "-selection", "clipboard", "-t", "text/uri-list", "-o"},
	} {
		if _, err := exec.LookPath(candidate[0]); err != nil {
			continue
		}
		out, err := exec.CommandContext(ctx, candidate[0], candidate[1:]...).Output()
		if err != nil {
			continue
		}
		if paths := parseFileURIList(out); len(paths) > 0 {
			return paths, nil
		}
	}
	return nil, nil
}

// parseFileURIList turns an RFC 2483 text/uri-list into absolute paths, keeping only
// file:// entries (an http:// URL on the clipboard is not something the agent can open
// from disk) and skipping the format's `#` comment lines.
func parseFileURIList(raw []byte) []string {
	var paths []string
	for _, line := range strings.Split(string(bytes.TrimSpace(raw)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		u, err := url.Parse(line)
		if err != nil || u.Scheme != "file" || u.Path == "" {
			continue
		}
		// u.Path is already percent-decoded by url.Parse; a remote host component means the
		// file is not on this disk, which is the one thing this whole path assumes.
		if u.Host != "" && u.Host != "localhost" {
			continue
		}
		paths = append(paths, u.Path)
	}
	return paths
}
