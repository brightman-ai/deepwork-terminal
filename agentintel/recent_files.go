package agentintel

import (
	"path/filepath"
	"sort"
	"time"

	"github.com/brightman-ai/kit/transcript"
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
	recentScanTailBytes = 4 << 20 // 4 MiB — tail window per transcript
	recentTranscriptN   = 3       // newest transcripts parsed per tool (covers /clear)
	codexProbeCap       = 40      // max Codex rollout heads probed when matching cwd
)

// The edit-tool allowlist + image-ext rule that used to live here (editToolNames /
// imageExts) is now the cross-repo SSOT in github.com/brightman-ai/kit/transcript
// (TouchedPath) — shared with pro's share/owner touched view so the rule can't drift.
// The kit set is the UNION (adds .svg; keeps .avif), a strict superset of the old.

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
	// Parse only the project's recentTranscriptN newest transcripts (Claude + Codex) for this
	// cwd, tail-read. NOT every project transcript or every Codex rollout (the old all-files
	// scan was the multi-second first load). N>1 keeps recent files visible across `/clear`,
	// since the cleared session's prior transcript is still among the newest N.
	var rows []RecentFile
	if files, err := pl.ClaudeSessionFiles(projectCWD); err == nil { // mtime newest-first
		for _, p := range capPaths(files, recentTranscriptN) {
			rows = append(rows, claudeEditRowsFromFile(p, "")...)
		}
	}
	for _, p := range recentCodexRolloutsForCWD(pl, projectCWD, recentTranscriptN) {
		rows = append(rows, codexEditRowsFromFile(p, projectCWD)...)
	}
	return dedupeAndCap(rows)
}

func capPaths(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// recentCodexRolloutsForCWD returns up to n newest Codex rollouts whose recorded cwd matches.
// It probes only each rollout's head (rolloutCWD reads the leading session_meta and stops
// early) and bounds how many files it inspects, so a Claude-only project doesn't pay a full
// Codex-history walk.
func recentCodexRolloutsForCWD(pl *ProjectLocator, cwd string, n int) []string {
	want := canonicalCWD(cwd)
	var out []string
	checked := 0
	for _, p := range pl.CodexSessionFiles() { // newest-mtime first
		if checked >= codexProbeCap {
			break
		}
		checked++
		if want == "" || canonicalCWD(rolloutCWD(p)) == want {
			out = append(out, p)
			if len(out) >= n {
				break
			}
		}
	}
	return out
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

// claudeEditRowsFromFile extracts edit-tool file paths from one transcript. wantCWD is
// normally "" (the file already belongs to the resolved project transcript); when non-empty
// only rows whose cwd matches are kept.
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
			input, ok := m["input"].(map[string]any)
			if !ok {
				continue
			}
			// Edit-tool allowlist = the kit/transcript domain-rule SSOT (Write/Edit/
			// MultiEdit/NotebookEdit via file_path; Read only for images; Bash excluded).
			// The tail-window scan stays here for perf; only the RULE is shared so it can
			// never drift from pro's share/owner touched view.
			fp, ok := transcript.TouchedPath(name, input)
			if !ok {
				continue
			}
			tool := name
			if name == "Read" {
				// counted by the rule ⇒ it was an image Read → the 图片 tab.
				tool = "read-image"
			}
			out = append(out, RecentFile{
				Path: fp,
				Name: filepath.Base(fp),
				Dir:  filepath.Dir(fp),
				Tool: tool,
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
func codexEditRowsFromFile(path, projectCWD string) []RecentFile {
	var out []RecentFile
	want := canonicalCWD(projectCWD)
	// One rollout (already resolved to this cwd). Full-read is fine for a single file; we
	// need the head (session_meta carries cwd) so tail-read doesn't apply here.
	{
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
