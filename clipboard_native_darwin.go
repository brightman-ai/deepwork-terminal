//go:build darwin

package terminal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// readClipboardFilePaths asks NSPasteboard for the file URLs the user last copied.
//
// Why a subprocess and not cgo: deepwork-pro reaches the same API through an ObjC block
// compiled into the binary, and that is not portable HERE. This repo ships with
// CGO_ENABLED=0 (.goreleaser.yaml) — that is what makes the static Linux cross-builds and
// the darwin universal binary possible from one machine. Linking AppKit would trade the
// entire release pipeline for ~500 ms per paste. osascript is on every macOS, needs no
// entitlement to read the pasteboard, and only runs when the user actually pastes a file.
//
// JavaScript-for-Automation rather than AppleScript for two reasons: it starts about
// twice as fast (measured 0.48-0.65 s vs 0.93-1.82 s on this machine), and it can emit
// JSON — which matters, because a macOS filename may legally contain a newline, so any
// line-delimited output format is a silent corruption waiting for the file that has one.
const pasteboardFilePathsJXA = `
ObjC.import('AppKit');
(function () {
  var pb = $.NSPasteboard.generalPasteboard;
  var opts = $.NSDictionary.dictionaryWithObjectForKey(
    $.NSNumber.numberWithBool(true), $.NSPasteboardURLReadingFileURLsOnlyKey);
  var urls = pb.readObjectsForClassesOptions($.NSArray.arrayWithObject($.NSURL), opts);
  if (!urls || urls.isNil()) return '[]';
  // urls.count comes back through the bridge as a STRING ("2"), so it must be parsed
  // before it is used as a loop bound — comparing a number against it works by accident
  // and stops working the moment the value reaches two digits.
  var n = parseInt(ObjC.unwrap(urls.count), 10) || 0;
  var out = [];
  for (var i = 0; i < n; i++) {
    var p = ObjC.unwrap(urls.objectAtIndex(i).path);
    if (p) out.push(String(p));
  }
  return JSON.stringify(out);
})();
`

func readClipboardFilePaths(ctx context.Context) ([]string, error) {
	cmd := exec.CommandContext(ctx, "/usr/bin/osascript", "-l", "JavaScript", "-")
	cmd.Stdin = strings.NewReader(pasteboardFilePathsJXA)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// A cancelled/expired context kills the child, which surfaces as a generic exit
		// error; report the real cause so a slow pasteboard doesn't read as a broken script.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("read pasteboard: %w", ctxErr)
		}
		return nil, fmt.Errorf("read pasteboard: %w: %s", err, strings.TrimSpace(stderr.String()))
	}

	out := bytes.TrimSpace(stdout.Bytes())
	if len(out) == 0 {
		return nil, nil
	}
	var paths []string
	if err := json.Unmarshal(out, &paths); err != nil {
		return nil, fmt.Errorf("read pasteboard: unexpected osascript output %q", clampForError(out))
	}
	return paths, nil
}

// clampForError keeps an unexpected-output error message readable — the whole point of
// including the output is to see WHAT arrived, and 200 bytes is enough to recognize it.
func clampForError(b []byte) string {
	const max = 200
	if len(b) > max {
		return string(b[:max]) + "…"
	}
	return string(b)
}
