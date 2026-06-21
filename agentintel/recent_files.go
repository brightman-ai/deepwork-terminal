package agentintel

import (
	"path/filepath"
	"sort"
	"time"
)

// RecentFilesCap bounds how many recently-touched files we surface. The drawer's
// 文件 tab is a quick-jump surface, not an archive — 30 newest is plenty and keeps
// the JSONL scan cheap.
const RecentFilesCap = 30

// recentScanTailBytes bounds how much of each transcript's TAIL we parse. Recent edits
// live near the end, so reading the last few MB finds the newest RecentFilesCap files
// without parsing a multi-MB (sometimes 30MB+) transcript from the start — the cause of
// the slow first load. recentScanMaxFiles bounds the cross-project fallback / Codex scans
// (file lists are mtime-sorted newest-first, so the recent slice carries recent edits).
const (
	recentScanTailBytes = 4 << 20 // 4 MiB per transcript
	recentScanMaxFiles  = 40
)

// editToolNames is the SSOT signal for "claude/codex 生成或修改的文件": tool_use
// blocks whose name is a structured file-edit tool carrying input.file_path. Bash
// is excluded on purpose (input.command 非结构化路径，易误判), per CHG-016 D2.
var editToolNames = map[string]bool{
	"Write":        true,
	"Edit":         true,
	"MultiEdit":    true,
	"NotebookEdit": true,
}

// RecentFile is one file an agent wrote/edited, attributed from a transcript
// tool_use row. tsMs is the row timestamp (newest occurrence wins on dedup). Size
// and Exists are stat'd by the caller — a vanished file is still listed with
// Exists=false (它可能已被删除，仍是有用的历史信号).
type RecentFile struct {
	Path  string `json:"path"`
	Name  string `json:"name"`
	Dir   string `json:"dir"`
	Tool  string `json:"tool"`
	TsMs  int64  `json:"tsMs"`
	Size  int64  `json:"size"`
	Exists bool  `json:"exists"`
}

// RecentEditedFiles scans every Claude transcript whose cwd matches projectCWD and
// extracts the file paths agents wrote/edited (Write/Edit/MultiEdit/NotebookEdit
// tool_use blocks → input.file_path). It dedupes by path keeping the NEWEST tsMs,
// sorts newest-first, and caps at RecentFilesCap. Size/Exists are NOT filled here
// (no os.Stat) — that is the handler's job, keeping this pure and unit-testable.
//
// Codex apply_patch / function_call paths are defensively attempted but, since the
// rollout format is not yet stable, missing data is simply skipped (never an error).
//
// A missing transcript store, a malformed line, or an unreadable file never fails —
// it just yields fewer (or zero) files. Empty is a valid answer.
func RecentEditedFiles(pl *ProjectLocator, projectCWD string) []RecentFile {
	rows := scanClaudeEditRows(pl, projectCWD)
	rows = append(rows, scanCodexEditRows(pl, projectCWD)...)
	return dedupeAndCap(rows)
}

// dedupeAndCap collapses rows to one-per-path keeping the newest timestamp, sorts
// newest-first, and caps at RecentFilesCap. Exported-shape result is metadata-only;
// the caller stats each path for Size/Exists.
func dedupeAndCap(rows []RecentFile) []RecentFile {
	best := make(map[string]RecentFile, len(rows))
	for _, rf := range rows {
		if rf.Path == "" {
			continue
		}
		if prev, ok := best[rf.Path]; !ok || rf.TsMs > prev.TsMs {
			best[rf.Path] = rf
		}
	}
	out := make([]RecentFile, 0, len(best))
	for _, rf := range best {
		out = append(out, rf)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].TsMs > out[j].TsMs })
	if len(out) > RecentFilesCap {
		out = out[:RecentFilesCap]
	}
	return out
}

// scanClaudeEditRows walks Claude transcripts for the target project and pulls out
// edit-tool file paths. We resolve the project's transcript directory directly from
// the cwd (ClaudeProjectDir encoding); if that dir is missing/empty we fall back to
// scanning ALL transcripts and filtering by each row's cwd, so the feature still
// works when a session's cwd doesn't map cleanly to an encoded project dir.
func scanClaudeEditRows(pl *ProjectLocator, projectCWD string) []RecentFile {
	var out []RecentFile

	// Primary: the project's own transcript dir (cheap, precise).
	files, err := pl.ClaudeSessionFiles(projectCWD)
	if err == nil && len(files) > 0 {
		for _, path := range files {
			out = append(out, claudeEditRowsFromFile(path, "")...)
		}
		return out
	}

	// Fallback: scan recent transcripts, filter by row cwd matching the project. Bounded to
	// the newest recentScanMaxFiles (the list is mtime-sorted) so this never walks the whole
	// history (1000+ transcripts) just because a cwd didn't map to an encoded project dir.
	want := canonicalCWD(projectCWD)
	all := pl.ClaudeAllSessionFiles()
	if len(all) > recentScanMaxFiles {
		all = all[:recentScanMaxFiles]
	}
	for _, path := range all {
		out = append(out, claudeEditRowsFromFile(path, want)...)
	}
	return out
}

// claudeEditRowsFromFile extracts edit-tool file paths from one transcript. When
// wantCWD is non-empty, only rows whose cwd matches are kept (used by the fallback
// scan); when empty, every edit row is kept (the file already belongs to the
// project's transcript dir).
func claudeEditRowsFromFile(path, wantCWD string) []RecentFile {
	var out []RecentFile
	reader := NewJSONLReader(path)
	// Tail-read: recent edits are near the end; this bounds the parse to the last few MB
	// instead of the whole (possibly 30MB+) transcript. Claude rows are self-contained
	// (each carries its own cwd + timestamp), so a tail window needs no file header.
	_ = reader.ReadTailFunc(recentScanTailBytes, func(row map[string]any) bool {
		if wantCWD != "" {
			cwd, _ := row["cwd"].(string)
			if canonicalCWD(cwd) != wantCWD {
				return true
			}
		}
		tsMs := parseRowTimestampMs(row["timestamp"])
		msg, ok := row["message"].(map[string]any)
		if !ok {
			return true
		}
		content, ok := msg["content"].([]any)
		if !ok {
			return true
		}
		for _, blk := range content {
			m, ok := blk.(map[string]any)
			if !ok {
				continue
			}
			if t, _ := m["type"].(string); t != "tool_use" {
				continue
			}
			name, _ := m["name"].(string)
			if !editToolNames[name] {
				continue
			}
			input, ok := m["input"].(map[string]any)
			if !ok {
				continue
			}
			fp, _ := input["file_path"].(string)
			if fp == "" {
				continue
			}
			out = append(out, RecentFile{
				Path: fp,
				Name: filepath.Base(fp),
				Dir:  filepath.Dir(fp),
				Tool: name,
				TsMs: tsMs,
			})
		}
		return true
	})
	return out
}

// scanCodexEditRows defensively attempts to pull file paths from Codex rollouts for
// the target project. Codex's apply_patch / function_call rollout shape is not yet
// stable, so this is best-effort: it recognizes a function_call payload whose name
// looks like a file edit and carries a file_path/path argument. Anything it can't
// confidently parse is skipped (never an error), per CHG-016 D2.
func scanCodexEditRows(pl *ProjectLocator, projectCWD string) []RecentFile {
	var out []RecentFile
	want := canonicalCWD(projectCWD)
	// Codex rollouts are global (not per-project); we must read each file's head
	// (session_meta carries cwd) so tail-read doesn't apply, but bound the count to the
	// newest recentScanMaxFiles so this isn't an unbounded all-history scan.
	files := pl.CodexSessionFiles()
	if len(files) > recentScanMaxFiles {
		files = files[:recentScanMaxFiles]
	}
	for _, path := range files {
		reader := NewJSONLReader(path)
		cwd := ""
		_ = reader.ReadNewFunc(func(row map[string]any) bool {
			payload, _ := row["payload"].(map[string]any)
			if payload == nil {
				return true
			}
			if rt, _ := row["type"].(string); rt == "session_meta" {
				if c, ok := payload["cwd"].(string); ok && c != "" {
					cwd = c
				}
				return true
			}
			// Only keep rows once we know this rollout belongs to the project.
			if want != "" && canonicalCWD(cwd) != want {
				return true
			}
			fp := codexEditPath(payload)
			if fp == "" {
				return true
			}
			out = append(out, RecentFile{
				Path: fp,
				Name: filepath.Base(fp),
				Dir:  filepath.Dir(fp),
				Tool: "codex",
				TsMs: parseRowTimestampMs(row["timestamp"]),
			})
			return true
		})
	}
	return out
}

// codexEditPath best-effort extracts a file path from a Codex payload. It probes
// the common shapes (a function_call with a file_path/path argument). Unknown → "".
func codexEditPath(payload map[string]any) string {
	// Direct file_path/path on the payload.
	if fp, ok := payload["file_path"].(string); ok && fp != "" {
		return fp
	}
	if fp, ok := payload["path"].(string); ok && fp != "" {
		return fp
	}
	// Nested args (function_call style).
	if args, ok := payload["arguments"].(map[string]any); ok {
		if fp, ok := args["file_path"].(string); ok && fp != "" {
			return fp
		}
		if fp, ok := args["path"].(string); ok && fp != "" {
			return fp
		}
	}
	return ""
}

// canonicalCWD normalizes a working dir for comparison: resolves symlinks when
// possible (so /tmp vs /private/tmp matches), else falls back to Clean. Empty → "".
func canonicalCWD(p string) string {
	if p == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return filepath.Clean(p)
}

// parseRowTimestampMs parses a transcript RFC3339 timestamp into epoch millis.
// Unknown/unparseable → 0 (sorts last). Mirrors inputs.go's parseTimestampMs but
// lives here to keep the agentintel package self-contained.
func parseRowTimestampMs(v any) int64 {
	s, ok := v.(string)
	if !ok || s == "" {
		return 0
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UnixMilli()
	}
	return 0
}
