package terminal

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Session review (git diff) service — read-only. Surfaces the changed files + per-file
// unified diff for the workbench cwd's repository, so a user can glance at "what did this
// agent change" without leaving the terminal. Scope = the WHOLE working tree (staged +
// unstaged + untracked), mirroring `git status`.
//
// Security: every git invocation passes its arguments as a SLICE to exec.Command — never a
// shell string — so a filename or path can't inject a command. The only client-supplied
// value that reaches git is the cwd, and that is first validated by workbenchCWD (an
// absolute, existing directory, or the session's own cwd). All per-file paths come from
// git's own output, not the client. The trust model is the same local single-user one the
// files service documents: the caller already holds the auth code + full shell access.

// gitCommandTimeout bounds any single git invocation so a pathological repo (huge untracked
// tree, slow filesystem) can't hang the request.
const gitCommandTimeout = 6 * time.Second

// Caps keep a giant changeset from flooding the response (mirrors the files search budget
// idiom). Beyond these the response is flagged Truncated and the client says "改动过多".
const (
	// gitDiffMaxFiles caps how many changed files we emit (and run a per-file diff for).
	gitDiffMaxFiles = 500
	// gitFileDiffMaxBytes caps a SINGLE file's stored unified diff; a larger one is clipped.
	gitFileDiffMaxBytes = 200 << 10 // 200 KiB
	// gitDiffTotalMaxBytes caps the SUM of all stored diffs; once exceeded, remaining files
	// are still listed (status + counts) but carry no diff body.
	gitDiffTotalMaxBytes = 3 << 20 // 3 MiB
)

// errNotGit signals the resolved cwd is not inside a git work tree.
var errNotGit = errors.New("not a git repository")

// gitDiffFile is one changed file in GET /git/diff.
type gitDiffFile struct {
	// Status is a single letter derived from git's porcelain XY code: M(odified) / A(dded) /
	// D(eleted) / R(enamed) / ? (untracked) — the client colors the row by it.
	Status string `json:"status"`
	// Path is the file path relative to the repo root, forward slashes, un-quoted (unicode-safe).
	Path string `json:"path"`
	// Orig is the pre-rename path (only set for a rename), so the UI can show "old → new".
	Orig string `json:"orig,omitempty"`
	// Added / Deleted are the +/- line counts parsed from the (full) unified diff.
	Added   int `json:"added"`
	Deleted int `json:"deleted"`
	// Binary marks a file whose change git couldn't render as a text diff.
	Binary bool `json:"binary,omitempty"`
	// Diff is the unified diff body (may be clipped — see Truncated — or empty when the total
	// budget was exhausted / the file is binary).
	Diff string `json:"diff"`
	// Truncated is true when THIS file's diff was clipped to gitFileDiffMaxBytes.
	Truncated bool `json:"truncated,omitempty"`
}

// gitDiffResponse is the GET /git/diff envelope. It always decodes to a valid shape so the
// client can render an empty state without special-casing an error status.
type gitDiffResponse struct {
	// Root is the repository top-level (may be an ANCESTOR of cwd when cwd is a subdirectory).
	Root string `json:"root"`
	// Files is the changed-file list (empty on a clean tree).
	Files []gitDiffFile `json:"files"`
	// NotGit is true when cwd resolved but isn't inside a git work tree ("非 git 仓库").
	NotGit bool `json:"notGit,omitempty"`
	// NoCwd is true when no working directory could be resolved at all ("cwd 缺失").
	NoCwd bool `json:"noCwd,omitempty"`
	// Truncated is true when the changeset hit a file-count or total-byte cap.
	Truncated bool `json:"truncated,omitempty"`
}

// handleGitDiff handles GET /git/diff?session=<id>&cwd=<dir>.
//
// It resolves the workbench cwd (client-supplied live pane cwd, else the session cwd),
// probes the enclosing git root (cwd may be a subdirectory), then returns the working
// tree's changed files + each file's unified diff. Soft-fails to a graceful empty state:
// no cwd → {noCwd}, not a repo → {notGit}, clean tree → empty Files — never a 5xx.
func (s *Server) handleGitDiff(w http.ResponseWriter, r *http.Request) {
	cwd, ok := s.workbenchCWD(r.Context(), r.URL.Query().Get("session"), r.URL.Query().Get("cwd"))
	if !ok || cwd == "" {
		writeJSON(w, http.StatusOK, gitDiffResponse{Files: []gitDiffFile{}, NoCwd: true})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), gitCommandTimeout)
	defer cancel()

	root, err := gitToplevel(ctx, cwd)
	if err != nil {
		writeJSON(w, http.StatusOK, gitDiffResponse{Files: []gitDiffFile{}, NotGit: true})
		return
	}

	files, truncated := gitChangedFiles(ctx, root)
	if files == nil {
		files = []gitDiffFile{}
	}
	writeJSON(w, http.StatusOK, gitDiffResponse{Root: root, Files: files, Truncated: truncated})
}

// runGit runs `git -C dir <args...>` with the given context, returning stdout. Args are
// passed as a slice (no shell) so filenames can't inject. --no-optional-locks + a disabled
// pager keep it side-effect-free; core.quotepath=false yields raw (unicode) paths.
func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	full := append([]string{"-C", dir, "--no-optional-locks", "-c", "core.quotepath=false"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_PAGER=cat", "GIT_TERMINAL_PROMPT=0")
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	err := cmd.Run()
	return out.String(), err
}

// runGitDiff runs a diff-family git command that legitimately exits 1 when differences are
// present (e.g. `diff --no-index`). It returns the captured stdout and whether the command
// succeeded (exit 0 or the benign exit 1); a real failure (git missing, exit ≥2) → ok=false.
func runGitDiff(ctx context.Context, dir string, args ...string) (string, bool) {
	out, err := runGit(ctx, dir, args...)
	if err == nil {
		return out, true
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 1 {
		return out, true // diffs present — expected for --no-index / plain diff
	}
	return out, false
}

// gitToplevel resolves the repository root that contains cwd (an ancestor when cwd is a
// subdirectory). An error (incl. "not a git repository") → errNotGit at the caller.
func gitToplevel(ctx context.Context, cwd string) (string, error) {
	out, err := runGit(ctx, cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", errNotGit
	}
	root := strings.TrimSpace(out)
	if root == "" {
		return "", errNotGit
	}
	return root, nil
}

// changed is one parsed `git status --porcelain -z` entry.
type changed struct {
	xy        string // 2-char porcelain code, e.g. " M", "A ", "R ", "??"
	path      string // new/current path (repo-relative)
	orig      string // pre-rename path (set only for R/C)
	untracked bool
}

// gitChangedFiles lists the working tree's changed files (staged + unstaged + untracked)
// and attaches each file's unified diff + line counts. Truncated is true when a cap was hit.
func gitChangedFiles(ctx context.Context, root string) ([]gitDiffFile, bool) {
	// Porcelain v1 with -z (NUL-delimited, no path quoting) and -uall (expand untracked dirs
	// into individual files) is the authoritative change set — the same command R-2 verifies
	// against. -z makes spaces/unicode in filenames safe to parse.
	statusOut, err := runGit(ctx, root, "status", "--porcelain=v1", "-z", "-uall")
	if err != nil {
		return []gitDiffFile{}, false
	}
	entries := parseStatusZ(statusOut)

	hasHEAD := false
	if _, herr := runGit(ctx, root, "rev-parse", "--verify", "-q", "HEAD"); herr == nil {
		hasHEAD = true
	}

	truncated := false
	if len(entries) > gitDiffMaxFiles {
		entries = entries[:gitDiffMaxFiles]
		truncated = true
	}

	total := 0
	files := make([]gitDiffFile, 0, len(entries))
	for _, e := range entries {
		f := gitDiffFile{Status: statusLetter(e.xy, e.untracked), Path: e.path, Orig: e.orig}

		// Once the total budget is spent, keep LISTING files (status/path) but stop producing
		// diff bodies — the list stays complete; only the (large) bodies are dropped.
		if total >= gitDiffTotalMaxBytes {
			truncated = true
			files = append(files, f)
			continue
		}

		diff, added, deleted, binary := fileDiff(ctx, root, e, hasHEAD)
		f.Added, f.Deleted, f.Binary = added, deleted, binary
		if len(diff) > gitFileDiffMaxBytes {
			diff = clipUTF8(diff, gitFileDiffMaxBytes) + "\n… (diff 已截断)\n"
			f.Truncated = true
			truncated = true
		}
		f.Diff = diff
		total += len(diff)
		files = append(files, f)
	}
	return files, truncated
}

// parseStatusZ splits `git status --porcelain=v1 -z` output into entries. Each record is
// `XY<space><path>`; a rename/copy record is followed by a SEPARATE record holding the
// original path, which we fold into the same entry (git emits the NEW path first).
func parseStatusZ(out string) []changed {
	tokens := strings.Split(strings.TrimRight(out, "\x00"), "\x00")
	var res []changed
	for i := 0; i < len(tokens); i++ {
		t := tokens[i]
		if len(t) < 4 { // need at least "XY p"
			continue
		}
		xy := t[:2]
		path := t[3:] // skip the separator byte at index 2
		e := changed{xy: xy, path: path, untracked: xy == "??"}
		// Rename/copy in either the index (X) or work tree (Y) → consume the origin path token.
		if xy[0] == 'R' || xy[0] == 'C' || xy[1] == 'R' || xy[1] == 'C' {
			if i+1 < len(tokens) {
				e.orig = tokens[i+1]
				i++
			}
		}
		res = append(res, e)
	}
	return res
}

// statusLetter reduces a porcelain XY code to a single display letter (M/A/D/R/? …). The
// staged (X) status wins when set, else the work-tree (Y) status; untracked → "?".
func statusLetter(xy string, untracked bool) string {
	if untracked {
		return "?"
	}
	x, y := xy[0], xy[1]
	if x != ' ' && x != '?' {
		return strings.ToUpper(string(x))
	}
	if y != ' ' {
		return strings.ToUpper(string(y))
	}
	return "?"
}

// fileDiff produces the unified diff + counts for one changed entry. Untracked files diff
// against /dev/null (all lines added); renames pass BOTH paths so rename detection renders
// "old → new"; everything else diffs against HEAD (or the index on a repo with no commits).
func fileDiff(ctx context.Context, root string, e changed, hasHEAD bool) (diff string, added, deleted int, binary bool) {
	var out string
	switch {
	case e.untracked:
		out, _ = runGitDiff(ctx, root, "diff", "--no-color", "--no-index", "--", os.DevNull, e.path)
	case e.orig != "" && hasHEAD:
		out, _ = runGitDiff(ctx, root, "diff", "--no-color", "HEAD", "--", e.orig, e.path)
	case hasHEAD:
		out, _ = runGitDiff(ctx, root, "diff", "--no-color", "HEAD", "--", e.path)
	default:
		// No commits yet: staged additions live in the index; --cached diffs them vs the empty tree.
		out, _ = runGitDiff(ctx, root, "diff", "--no-color", "--cached", "--", e.path)
	}
	added, deleted, binary = countDiff(out)
	return out, added, deleted, binary
}

// countDiff tallies added/deleted lines from a unified diff and detects a binary change.
// It ignores the +++/--- file headers and only counts hunk body lines.
func countDiff(diff string) (added, deleted int, binary bool) {
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---"):
			// file header — not a content line
		case strings.HasPrefix(line, "+"):
			added++
		case strings.HasPrefix(line, "-"):
			deleted++
		case strings.HasPrefix(line, "Binary files ") || strings.HasPrefix(line, "GIT binary patch"):
			binary = true
		}
	}
	return added, deleted, binary
}

// clipUTF8 truncates s to at most n bytes without splitting a multi-byte UTF-8 rune.
func clipUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && (s[n]&0xC0) == 0x80 { // back up over UTF-8 continuation bytes
		n--
	}
	return s[:n]
}
