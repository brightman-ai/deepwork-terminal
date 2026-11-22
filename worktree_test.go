package terminal

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newWorktreeProfileTestServer(t *testing.T) (*httptest.Server, *SessionManager, *Server) {
	t.Helper()
	server, sm, srv := newDrawerTestServer(t)
	srv.registerWorktreeAndProfileRoutes(srv.authWrap)
	return server, sm, srv
}

func seedCleanWorktreeRepo(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "demo-repo")
	require.NoError(t, os.MkdirAll(repo, 0o755))
	gitCLI(t, repo, "init")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "README.md"), []byte("# demo\nline2\n"), 0o644))
	gitCLI(t, repo, "add", "-A")
	gitCLI(t, repo, "commit", "-m", "initial commit")
	return repo
}

func TestWorktree_ListAllGroupsLinkedCheckoutsAndSortsRepos(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	srv := newIsolatedBotTestServer(t)
	srv.registerWorktreeAndProfileRoutes(nil)
	type response struct{ Repos []worktreeRepo }
	empty := doJSONReq(t, srv, http.MethodGet, "/git/worktrees/all", nil)
	require.Equal(t, http.StatusOK, empty.Code)
	var emptyResult response
	require.NoError(t, json.NewDecoder(empty.Body).Decode(&emptyResult))
	assert.NotNil(t, emptyResult.Repos)
	assert.Empty(t, emptyResult.Repos)

	makeRepo := func(name string) string {
		old := seedCleanWorktreeRepo(t)
		dir := filepath.Join(filepath.Dir(old), name)
		require.NoError(t, os.Rename(old, dir))
		return canonicalPath(dir)
	}
	busy, alpha, beta, extra := makeRepo("z-busy"), makeRepo("alpha"), makeRepo("beta"), makeRepo("extra")
	linked := filepath.Join(canonicalPath(t.TempDir()), "working")
	gitCLI(t, busy, "worktree", "add", "-b", "topic/linked", linked)
	subdir := filepath.Join(busy, "subdir")
	require.NoError(t, os.Mkdir(subdir, 0o755))
	seedSessionInManager(srv.mgr, "first-linked", linked, "shell", nil)
	seedSessionInManager(srv.mgr, "second-subdir", subdir, "shell", nil)
	seedSessionInManager(srv.mgr, "third-root", busy, "shell", nil)
	seedSessionInManager(srv.mgr, "fourth-root", busy, "shell", nil)
	seedSessionInManager(srv.mgr, "fifth-beta", beta, "shell", nil)
	seedSessionInManager(srv.mgr, "sixth-alpha", alpha, "shell", nil)
	seedSessionInManager(srv.mgr, "not-git", t.TempDir(), "shell", nil)

	res := doJSONReq(t, srv, http.MethodGet, "/git/worktrees/all?cwd="+url.QueryEscape(extra), nil)
	require.Equal(t, http.StatusOK, res.Code)
	var result response
	require.NoError(t, json.NewDecoder(res.Body).Decode(&result))
	require.Len(t, result.Repos, 4)
	assert.Equal(t, []string{busy, alpha, beta, extra}, []string{result.Repos[0].Root, result.Repos[1].Root, result.Repos[2].Root, result.Repos[3].Root})
	assert.Equal(t, []int{4, 1, 1, 0}, []int{result.Repos[0].SessionCount, result.Repos[1].SessionCount, result.Repos[2].SessionCount, result.Repos[3].SessionCount})
	require.Len(t, result.Repos[0].Worktrees, 2)
	assert.True(t, result.Repos[0].Worktrees[0].IsPrimary)
	assert.Equal(t, linked, result.Repos[0].Worktrees[1].Path)
	assert.Equal(t, "topic/linked", result.Repos[0].Worktrees[1].Branch)

	duplicate := doJSONReq(t, srv, http.MethodGet, "/git/worktrees/all?cwd="+url.QueryEscape(linked), nil)
	require.Equal(t, http.StatusOK, duplicate.Code)
	require.NoError(t, json.NewDecoder(duplicate.Body).Decode(&result))
	require.Len(t, result.Repos, 3)
	assert.Equal(t, 4, result.Repos[0].SessionCount)
}

func TestWorktree_ListBoundariesAndAuth(t *testing.T) {
	server, sm, srv := newWorktreeProfileTestServer(t)

	// Calling registerWorktreeAndProfileRoutes a second time on the same mux must be idempotent.
	require.NotPanics(t, func() {
		srv.registerWorktreeAndProfileRoutes(srv.authWrap)
	})

	// Unauthenticated call is rejected by authWrap.
	unauthReq, err := http.NewRequest(http.MethodGet, server.URL+"/git/worktrees", nil)
	require.NoError(t, err)
	unauthResp, err := http.DefaultClient.Do(unauthReq)
	require.NoError(t, err)
	unauthResp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, unauthResp.StatusCode)

	// Unknown session + no cwd -> 200 soft-fail with noCwd: true.
	resp, err := httpGet(server.URL+"/git/worktrees?session=missing-sess", "")
	require.NoError(t, err)
	var noCwdOut worktreeListResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&noCwdOut))
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.True(t, noCwdOut.NoCwd)
	assert.Empty(t, noCwdOut.Worktrees)

	// Non-git directory -> 200 soft-fail with notGit: true.
	plainDir := t.TempDir()
	_, err = sm.CreateWithOptions(CreateOptions{Name: "plain-wt", CWD: plainDir})
	require.NoError(t, err)
	plainSess := sessionByName(t, sm, "plain-wt")

	q := "/git/worktrees?session=" + url.QueryEscape(plainSess.ID) + "&cwd=" + url.QueryEscape(plainDir)
	resp, err = httpGet(server.URL+q, "")
	require.NoError(t, err)
	var notGitOut worktreeListResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&notGitOut))
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.True(t, notGitOut.NotGit)
	assert.Empty(t, notGitOut.Worktrees)
}

func TestWorktree_CreateListDirtyAndMergeLifecycle(t *testing.T) {
	server, sm, srv := newWorktreeProfileTestServer(t)
	repo := seedCleanWorktreeRepo(t)

	_, err := sm.CreateWithOptions(CreateOptions{Name: "primary", CWD: repo})
	require.NoError(t, err)
	sess := sessionByName(t, sm, "primary")

	listWorktrees := func() worktreeListResponse {
		t.Helper()
		q := "/git/worktrees?session=" + url.QueryEscape(sess.ID) + "&cwd=" + url.QueryEscape(repo)
		resp, reqErr := httpGet(server.URL+q, "")
		require.NoError(t, reqErr)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var out worktreeListResponse
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
		return out
	}

	// 1. Initial repo has only the primary checkout.
	initial := listWorktrees()
	require.Len(t, initial.Worktrees, 1)
	assert.True(t, initial.Worktrees[0].IsPrimary)
	assert.Equal(t, "main", initial.Worktrees[0].Branch)
	assert.False(t, initial.Worktrees[0].Dirty)
	assert.False(t, initial.Worktrees[0].Merged)

	// 2. Create an isolated CoW worktree under <dataDir>/worktrees/<repoBase>/<slug>.
	createBody, _ := json.Marshal(map[string]any{
		"session": sess.ID,
		"cwd":     repo,
		"branch":  "feat/cow-slice",
	})
	resp, err := httpPost(server.URL+"/git/worktrees", string(createBody), "")
	require.NoError(t, err)
	rawCreate, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode, "body: %s", string(rawCreate))

	var created struct {
		Path      string `json:"path"`
		Branch    string `json:"branch"`
		BaseRef   string `json:"baseRef"`
		IsPrimary bool   `json:"isPrimary"`
	}
	require.NoError(t, json.Unmarshal(rawCreate, &created))
	assert.Equal(t, "feat/cow-slice", created.Branch)
	assert.Equal(t, "main", created.BaseRef)
	assert.False(t, created.IsPrimary)
	assert.True(t, strings.HasPrefix(created.Path, filepath.Join(srv.dataDir(), "worktrees", "demo-repo")),
		"worktree must live under <dataDir>/worktrees/<repoBase>: got %s", created.Path)

	// 3. Newly created worktree (0 commits ahead) must NOT report merged: true prematurely.
	afterCreate := listWorktrees()
	require.Len(t, afterCreate.Worktrees, 2)
	wt := afterCreate.Worktrees[1]
	assert.False(t, wt.IsPrimary)
	assert.Equal(t, "feat/cow-slice", wt.Branch)
	assert.False(t, wt.Dirty)
	assert.False(t, wt.Merged)

	// 4. Uncommitted change marks worktree dirty and blocks merge with HTTP 409.
	featureFile := filepath.Join(created.Path, "feature.txt")
	require.NoError(t, os.WriteFile(featureFile, []byte("shipped via worktree\n"), 0o644))

	dirtyState := listWorktrees()
	require.Len(t, dirtyState.Worktrees, 2)
	assert.True(t, dirtyState.Worktrees[1].Dirty)

	mergeReqBody, _ := json.Marshal(map[string]any{"cwd": created.Path})
	dirtyMergeResp, err := httpPost(server.URL+"/git/worktrees/merge", string(mergeReqBody), "")
	require.NoError(t, err)
	dirtyMergeResp.Body.Close()
	assert.Equal(t, http.StatusConflict, dirtyMergeResp.StatusCode)

	// 5. Commit in worktree -> clean, then merge --no-ff into primary checkout succeeds.
	gitCLI(t, created.Path, "add", "feature.txt")
	gitCLI(t, created.Path, "commit", "-m", "feat: add feature.txt")

	mergeResp, err := httpPost(server.URL+"/git/worktrees/merge", string(mergeReqBody), "")
	require.NoError(t, err)
	rawMerge, err := io.ReadAll(mergeResp.Body)
	mergeResp.Body.Close()
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, mergeResp.StatusCode, "body: %s", string(rawMerge))

	var mergedOut struct {
		OK           bool   `json:"ok"`
		MergedBranch string `json:"mergedBranch"`
		TargetBranch string `json:"targetBranch"`
	}
	require.NoError(t, json.Unmarshal(rawMerge, &mergedOut))
	assert.True(t, mergedOut.OK)
	assert.Equal(t, "feat/cow-slice", mergedOut.MergedBranch)
	assert.Equal(t, "main", mergedOut.TargetBranch)

	// Primary checkout now contains feature.txt, and GET /git/worktrees marks the worktree merged.
	gotContent, err := os.ReadFile(filepath.Join(repo, "feature.txt"))
	require.NoError(t, err)
	assert.Equal(t, "shipped via worktree\n", string(gotContent))

	afterMerge := listWorktrees()
	require.Len(t, afterMerge.Worktrees, 2)
	assert.True(t, afterMerge.Worktrees[1].Merged, "merged worktree should report merged=true")
}

func TestWorktree_MergeConflictAbortsCleanly(t *testing.T) {
	server, _, _ := newWorktreeProfileTestServer(t)
	repo := seedCleanWorktreeRepo(t)

	createBody, _ := json.Marshal(map[string]any{
		"cwd":    repo,
		"branch": "fix/conflict-case",
	})
	resp, err := httpPost(server.URL+"/git/worktrees", string(createBody), "")
	require.NoError(t, err)
	var created struct {
		Path string `json:"path"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	// Create conflicting commits on main vs fix/conflict-case.
	require.NoError(t, os.WriteFile(filepath.Join(repo, "README.md"), []byte("main edit\n"), 0o644))
	gitCLI(t, repo, "add", "README.md")
	gitCLI(t, repo, "commit", "-m", "main change")

	require.NoError(t, os.WriteFile(filepath.Join(created.Path, "README.md"), []byte("worktree conflicting edit\n"), 0o644))
	gitCLI(t, created.Path, "add", "README.md")
	gitCLI(t, created.Path, "commit", "-m", "wt change")

	mergeBody, _ := json.Marshal(map[string]any{"cwd": created.Path})
	mResp, err := httpPost(server.URL+"/git/worktrees/merge", string(mergeBody), "")
	require.NoError(t, err)
	mResp.Body.Close()
	assert.Equal(t, http.StatusConflict, mResp.StatusCode)

	// Verify primary repo aborted cleanly and is not left dirty/conflicted.
	q := "/git/worktrees?cwd=" + url.QueryEscape(repo)
	lResp, err := httpGet(server.URL+q, "")
	require.NoError(t, err)
	var listed worktreeListResponse
	require.NoError(t, json.NewDecoder(lResp.Body).Decode(&listed))
	lResp.Body.Close()
	require.NotEmpty(t, listed.Worktrees)
	assert.False(t, listed.Worktrees[0].Dirty, "primary repo must stay clean after aborted conflict")
}

func TestWorktree_MergeRefusesBranchSwitchWithActivePrimarySession(test *testing.T) {
	server, manager, _ := newWorktreeProfileTestServer(test)
	repo := seedCleanWorktreeRepo(test)
	source := filepath.Join(test.TempDir(), "source")
	gitCLI(test, repo, "branch", "target")
	gitCLI(test, repo, "worktree", "add", "-b", "feature", source, "main")
	nested := filepath.Join(repo, "nested")
	require.NoError(test, os.Mkdir(nested, 0o755))
	sess, err := manager.CreateWithOptions(CreateOptions{Name: "primary-active", CWD: nested})
	require.NoError(test, err)

	body, err := json.Marshal(map[string]string{"cwd": source, "targetBranch": "target"})
	require.NoError(test, err)
	merge := func() (int, string) {
		test.Helper()
		resp, err := httpPost(server.URL+"/git/worktrees/merge", string(body), "")
		require.NoError(test, err)
		defer resp.Body.Close()
		var out struct {
			Code string `json:"code"`
		}
		require.NoError(test, json.NewDecoder(resp.Body).Decode(&out))
		return resp.StatusCode, out.Code
	}
	status, code := merge()
	require.Equal(test, http.StatusConflict, status)
	require.Equal(test, "active_session", code)
	worktrees, err := parseWorktreeList(test.Context(), repo)
	require.NoError(test, err)
	require.NotEmpty(test, worktrees)
	require.Equal(test, "main", worktrees[0].Branch)

	require.NoError(test, manager.Destroy(sess.ID))
	status, _ = merge()
	require.Equal(test, http.StatusOK, status)
	worktrees, err = parseWorktreeList(test.Context(), repo)
	require.NoError(test, err)
	require.NotEmpty(test, worktrees)
	require.Equal(test, "target", worktrees[0].Branch)
}

func TestWorktree_RemoveThreeHardGuardsAndForceCleanup(t *testing.T) {
	server, sm, _ := newWorktreeProfileTestServer(t)
	repo := seedCleanWorktreeRepo(t)

	postRemove := func(path string, force bool) (int, map[string]any) {
		t.Helper()
		b, _ := json.Marshal(map[string]any{"path": path, "force": force})
		resp, err := httpPost(server.URL+"/git/worktrees/remove", string(b), "")
		require.NoError(t, err)
		defer resp.Body.Close()
		var payload map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&payload)
		return resp.StatusCode, payload
	}

	// Guard 1: primary_protected — unconditionally rejected even when force=true.
	for _, force := range []bool{false, true} {
		code, body := postRemove(repo, force)
		assert.Equal(t, http.StatusConflict, code)
		assert.Equal(t, "primary_protected", body["code"])
	}

	// Create two linked worktrees to test dirty_worktree and active_session guards.
	createWT := func(branch string) string {
		t.Helper()
		b, _ := json.Marshal(map[string]any{"cwd": repo, "branch": branch})
		resp, err := httpPost(server.URL+"/git/worktrees", string(b), "")
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusCreated, resp.StatusCode)
		var out struct {
			Path string `json:"path"`
		}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
		return out.Path
	}

	wtDirty := createWT("feat/dirty-guard")
	require.NoError(t, os.WriteFile(filepath.Join(wtDirty, "uncommitted.txt"), []byte("wip\n"), 0o644))

	// Guard 2: dirty_worktree when !force.
	code, body := postRemove(wtDirty, false)
	assert.Equal(t, http.StatusConflict, code)
	assert.Equal(t, "dirty_worktree", body["code"])

	// Force removal succeeds on non-primary dirty worktree.
	code, body = postRemove(wtDirty, true)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, true, body["ok"])
	_, statErr := os.Stat(wtDirty)
	assert.True(t, os.IsNotExist(statErr), "dirty worktree removed when force=true")

	// Guard 3: active_session when !force (session CWD inside subdirectory of worktree).
	wtActive := createWT("feat/active-sess-guard")
	subDir := filepath.Join(wtActive, "nested")
	require.NoError(t, os.MkdirAll(subDir, 0o755))
	gitCLI(t, wtActive, "add", "-A") // keep worktree clean (empty dir isn't tracked anyway)

	sess, err := sm.CreateWithOptions(CreateOptions{Name: "active-in-wt", CWD: subDir})
	require.NoError(t, err)

	code, body = postRemove(wtActive, false)
	assert.Equal(t, http.StatusConflict, code)
	assert.Equal(t, "active_session", body["code"])

	// Once the active session is destroyed, clean removal succeeds without force.
	require.NoError(t, sm.Destroy(sess.ID))
	code, body = postRemove(wtActive, false)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, true, body["ok"])
	_, statErr = os.Stat(wtActive)
	assert.True(t, os.IsNotExist(statErr))
}

func TestWorktree_RejectsFlagInjectionAndInvalidBranchNames(t *testing.T) {
	server, _, _ := newWorktreeProfileTestServer(t)
	repo := seedCleanWorktreeRepo(t)

	badInputs := []map[string]any{
		{"cwd": repo, "branch": "--upload-pack=evil"},
		{"cwd": repo, "branch": "-f"},
		{"cwd": repo, "branch": "../escape"},
		{"cwd": repo, "branch": "feat/bad..name"},
		{"cwd": repo, "branch": "feat/ok", "baseRef": "--force"},
	}
	for _, payload := range badInputs {
		raw, _ := json.Marshal(payload)
		resp, err := httpPost(server.URL+"/git/worktrees", string(raw), "")
		require.NoError(t, err)
		resp.Body.Close()
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "payload should be rejected: %v", payload)
	}
}

func TestWorktree_CollectFiltersEphemeralTempWorktrees(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	repo := canonicalPath(seedCleanWorktreeRepo(t))
	dataDir := canonicalPath(t.TempDir())
	scratchBase := canonicalPath(t.TempDir())

	deadTempWT := filepath.Join(scratchBase, "scratch-dead")
	sessionTempWT := filepath.Join(scratchBase, "scratch-active")
	managedWT := filepath.Join(dataDir, "worktrees", "demo-repo", "managed-slice")
	require.NoError(t, os.MkdirAll(filepath.Dir(managedWT), 0o755))

	gitCLI(t, repo, "worktree", "add", "-b", "topic/dead-tmp", deadTempWT)
	gitCLI(t, repo, "worktree", "add", "-b", "topic/session-tmp", sessionTempWT)
	gitCLI(t, repo, "worktree", "add", "-b", "topic/managed", managedWT)

	nestedSessionDir := filepath.Join(sessionTempWT, "sub")
	require.NoError(t, os.MkdirAll(nestedSessionDir, 0o755))

	cwds := []sessionWorktreeCWD{
		{id: "sess-primary", cwd: repo},
		{id: "sess-tmp", cwd: nestedSessionDir},
	}

	repos, complete := collectWorktreeReposWithOptions(t.Context(), cwds, dataDir, true)
	require.True(t, complete)
	require.Len(t, repos, 1)

	gotPaths := make([]string, 0, len(repos[0].Worktrees))
	for _, wt := range repos[0].Worktrees {
		gotPaths = append(gotPaths, canonicalPath(wt.Path))
	}
	assert.Contains(t, gotPaths, repo, "primary checkout must be kept")
	assert.Contains(t, gotPaths, managedWT, "<dataDir>/worktrees/* must be kept")
	assert.Contains(t, gotPaths, sessionTempWT, "/tmp worktree with an active session CWD must be kept")
	assert.NotContains(t, gotPaths, deadTempWT, "dead /tmp worktree with no session must be filtered out")
}
