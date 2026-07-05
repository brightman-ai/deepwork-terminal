package terminal

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gitCLI runs a git command in dir for TEST SETUP (identity flags baked in so it works on a
// bare CI box with no global git config).
func gitCLI(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{"-C", dir,
		"-c", "user.email=t@t.t", "-c", "user.name=t", "-c", "commit.gpgsign=false",
		"-c", "init.defaultBranch=main"}, args...)
	cmd := exec.Command("git", full...)
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "git %v: %s", args, out)
}

// seedGitRepo makes dir a git repo with one commit, then leaves a mixed working tree:
// a modified tracked file, a staged-new file, a deletion, a rename, and an untracked file.
func seedGitRepo(t *testing.T, dir string) {
	t.Helper()
	gitCLI(t, dir, "init")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("l1\nl2\nl3\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "gone.txt"), []byte("bye\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "orig.txt"), []byte("a\nb\n"), 0o644))
	gitCLI(t, dir, "add", "-A")
	gitCLI(t, dir, "commit", "-m", "init")

	// Working-tree changes across all four kinds.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("l1\nCHANGED\nl3\nl4\n"), 0o644))
	require.NoError(t, os.Remove(filepath.Join(dir, "gone.txt")))
	gitCLI(t, dir, "mv", "sub/orig.txt", "sub/renamed.txt")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "staged_new.txt"), []byte("fresh\n"), 0o644))
	gitCLI(t, dir, "add", "staged_new.txt")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("u1\nu2\n"), 0o644))
}

func getGitDiff(t *testing.T, server, sessID, cwd string) gitDiffResponse {
	t.Helper()
	q := "/git/diff?session=" + url.QueryEscape(sessID) + "&cwd=" + url.QueryEscape(cwd)
	resp, err := httpGet(server+q, "")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var out gitDiffResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return out
}

// TC-GIT-01: parseStatusZ folds a rename's origin token into the same entry and flags untracked.
func TestParseStatusZ(t *testing.T) {
	// A  staged.txt | R  new.txt (orig old.txt) |  M mod.txt | ?? untracked.txt
	raw := "A  staged.txt\x00R  new.txt\x00old.txt\x00 M mod.txt\x00?? untracked.txt\x00"
	got := parseStatusZ(raw)
	require.Len(t, got, 4)

	assert.Equal(t, "staged.txt", got[0].path)
	assert.Equal(t, "A ", got[0].xy)
	assert.False(t, got[0].untracked)

	assert.Equal(t, "new.txt", got[1].path)
	assert.Equal(t, "old.txt", got[1].orig, "rename origin token folded in")

	assert.Equal(t, "mod.txt", got[2].path)

	assert.Equal(t, "untracked.txt", got[3].path)
	assert.True(t, got[3].untracked)
}

// TC-GIT-02: statusLetter reduces XY to a single display letter.
func TestStatusLetter(t *testing.T) {
	assert.Equal(t, "A", statusLetter("A ", false))
	assert.Equal(t, "M", statusLetter(" M", false))
	assert.Equal(t, "M", statusLetter("M ", false))
	assert.Equal(t, "D", statusLetter(" D", false))
	assert.Equal(t, "R", statusLetter("R ", false))
	assert.Equal(t, "?", statusLetter("??", true))
}

// TC-GIT-03: countDiff tallies +/- body lines, ignores file headers, and detects binary.
func TestCountDiff(t *testing.T) {
	diff := "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1,2 +1,3 @@\n line0\n-old\n+new1\n+new2\n"
	a, d, bin := countDiff(diff)
	assert.Equal(t, 2, a)
	assert.Equal(t, 1, d)
	assert.False(t, bin)

	_, _, bin2 := countDiff("diff --git a/i.png b/i.png\nBinary files a/i.png and b/i.png differ\n")
	assert.True(t, bin2)
}

// TC-GIT-04: GET /git/diff lists the whole working tree (staged+unstaged+untracked+rename+
// delete) with per-file unified diffs. Exercises R-1/R-2/R-3.
func TestGitDiff_Handler_RealRepo(t *testing.T) {
	server, sm, _ := newDrawerTestServer(t)
	cwd := t.TempDir()
	seedGitRepo(t, cwd)
	_, err := sm.CreateWithOptions(CreateOptions{Name: "gd", CWD: cwd})
	require.NoError(t, err)
	sess := sessionByName(t, sm, "gd")

	out := getGitDiff(t, server.URL, sess.ID, cwd)
	assert.False(t, out.NotGit)
	assert.False(t, out.NoCwd)
	assert.NotEmpty(t, out.Root)

	byPath := map[string]gitDiffFile{}
	for _, f := range out.Files {
		byPath[f.Path] = f
	}

	// Modified tracked file: M, with +/- counts and a real hunk in the diff body.
	mod, ok := byPath["tracked.txt"]
	require.True(t, ok, "modified file listed")
	assert.Equal(t, "M", mod.Status)
	assert.Greater(t, mod.Added, 0)
	assert.Greater(t, mod.Deleted, 0)
	assert.Contains(t, mod.Diff, "+CHANGED")

	// Untracked file: ? with an all-added diff vs /dev/null.
	unt, ok := byPath["untracked.txt"]
	require.True(t, ok, "untracked file listed")
	assert.Equal(t, "?", unt.Status)
	assert.Contains(t, unt.Diff, "+u1")

	// Staged-new file: A.
	stg, ok := byPath["staged_new.txt"]
	require.True(t, ok, "staged-new file listed")
	assert.Equal(t, "A", stg.Status)

	// Deletion: D.
	del, ok := byPath["gone.txt"]
	require.True(t, ok, "deleted file listed")
	assert.Equal(t, "D", del.Status)

	// Rename: R, carries the origin path.
	ren, ok := byPath["sub/renamed.txt"]
	require.True(t, ok, "renamed file listed")
	assert.Equal(t, "R", ren.Status)
	assert.Equal(t, "sub/orig.txt", ren.Orig)
}

// TC-GIT-05 (R-4): boundaries — non-git dir → notGit, clean repo → empty, unknown session → noCwd.
func TestGitDiff_Boundaries(t *testing.T) {
	server, sm, _ := newDrawerTestServer(t)

	// Non-git directory → notGit, empty files, never a 5xx.
	plain := t.TempDir()
	_, err := sm.CreateWithOptions(CreateOptions{Name: "plain", CWD: plain})
	require.NoError(t, err)
	plainSess := sessionByName(t, sm, "plain")
	nog := getGitDiff(t, server.URL, plainSess.ID, plain)
	assert.True(t, nog.NotGit, "non-git dir → notGit")
	assert.Empty(t, nog.Files)

	// Clean repo (committed, no changes) → NOT notGit, empty file list ("无改动").
	clean := t.TempDir()
	gitCLI(t, clean, "init")
	require.NoError(t, os.WriteFile(filepath.Join(clean, "a.txt"), []byte("x\n"), 0o644))
	gitCLI(t, clean, "add", "-A")
	gitCLI(t, clean, "commit", "-m", "init")
	_, err = sm.CreateWithOptions(CreateOptions{Name: "clean", CWD: clean})
	require.NoError(t, err)
	cleanSess := sessionByName(t, sm, "clean")
	cl := getGitDiff(t, server.URL, cleanSess.ID, clean)
	assert.False(t, cl.NotGit, "committed repo is a git repo")
	assert.Empty(t, cl.Files, "clean tree → no changed files")
	assert.NotEmpty(t, cl.Root)

	// Unknown session AND no cwd → noCwd (graceful, not an error).
	q := "/git/diff?session=" + url.QueryEscape("does-not-exist")
	resp, err := httpGet(server.URL+q, "")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var out gitDiffResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	assert.True(t, out.NoCwd, "unknown session + no cwd → noCwd")
}

// TC-GIT-06 (R): cwd is a SUBDIRECTORY of the repo — the root is probed via --show-toplevel
// and the whole tree's changes are still returned (paths stay repo-root-relative).
func TestGitDiff_CwdIsSubdir(t *testing.T) {
	server, sm, _ := newDrawerTestServer(t)
	root := t.TempDir()
	seedGitRepo(t, root)
	sub := filepath.Join(root, "sub")
	_, err := sm.CreateWithOptions(CreateOptions{Name: "sd", CWD: sub})
	require.NoError(t, err)
	sess := sessionByName(t, sm, "sd")

	out := getGitDiff(t, server.URL, sess.ID, sub)
	assert.False(t, out.NotGit)
	// A change at the repo ROOT (tracked.txt) is visible even though cwd is the subdir.
	var sawRoot bool
	for _, f := range out.Files {
		if f.Path == "tracked.txt" {
			sawRoot = true
		}
	}
	assert.True(t, sawRoot, "changes above cwd are included (root probed from subdir)")
}
