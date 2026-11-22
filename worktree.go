package terminal

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const worktreeOpTimeout = 15 * time.Second

// WorktreeInfo describes a single Git worktree entry returned by GET /git/worktrees.
type WorktreeInfo struct {
	Path             string `json:"path"`
	Branch           string `json:"branch"`
	Head             string `json:"head"`
	IsPrimary        bool   `json:"isPrimary"`
	Dirty            bool   `json:"dirty"`
	Merged           bool   `json:"merged"`
	CacheMB          int    `json:"cacheMB"`
	statusUnreadable bool
}

type worktreeListResponse struct {
	Root      string         `json:"root"`
	NotGit    bool           `json:"notGit,omitempty"`
	NoCwd     bool           `json:"noCwd,omitempty"`
	Worktrees []WorktreeInfo `json:"worktrees"`
}

type worktreeRepo struct {
	Root           string         `json:"root"`
	Name           string         `json:"name"`
	Worktrees      []WorktreeInfo `json:"worktrees"`
	SessionCount   int            `json:"sessionCount"`
	firstSessionID string
}

type sessionWorktreeCWD struct {
	id, cwd string
}

func (s *Server) handleListAllWorktrees(w http.ResponseWriter, r *http.Request) {
	var sessions []*Session
	if s.mgr != nil {
		sessions = s.mgr.List()
	}
	cwds := make([]sessionWorktreeCWD, 0, len(sessions)+1)
	for _, sess := range sessions {
		cwds = append(cwds, sessionWorktreeCWD{id: sess.ID, cwd: sessionWorktreeDir(sess)})
	}
	if cwd := strings.TrimSpace(r.URL.Query().Get("cwd")); cwd != "" {
		cwds = append(cwds, sessionWorktreeCWD{cwd: cwd})
	}
	ctx, cancel := context.WithTimeout(r.Context(), worktreeOpTimeout)
	defer cancel()
	repos, _ := s.collectWorktreeRepos(ctx, cwds)
	writeJSON(w, http.StatusOK, map[string]any{"repos": repos})
}

func sessionWorktreeDir(sess *Session) string {
	if cwd := processCWD(sess.ShellPID()); cwd != "" {
		return cwd
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return sess.CWD
}

func (s *Server) collectWorktreeRepos(ctx context.Context, cwds []sessionWorktreeCWD) ([]worktreeRepo, bool) {
	var dataDir string
	if s != nil {
		dataDir = s.dataDir()
	}
	return collectWorktreeReposWithOptions(ctx, cwds, dataDir, !strings.HasSuffix(os.Args[0], ".test"))
}

func collectWorktreeReposWithOptions(ctx context.Context, cwds []sessionWorktreeCWD, dataDir string, filterEphemeralTemp bool) ([]worktreeRepo, bool) {
	byCWD := make(map[string]*worktreeRepo)
	byCheckout := make(map[string]string)
	byCommonDir := make(map[string]*worktreeRepo)
	sessionCWDs := make([]string, 0, len(cwds))
	complete := true
	for _, source := range cwds {
		if ctx.Err() != nil {
			complete = false
			break
		}
		if !filepath.IsAbs(source.cwd) {
			continue
		}
		cwd := canonicalPath(source.cwd)
		if source.id != "" {
			sessionCWDs = append(sessionCWDs, cwd)
		}
		repo, known := byCWD[cwd]
		if !known {
			byCWD[cwd] = nil
			checkout, common, resolved := worktreeRepoKey(ctx, cwd, byCheckout)
			complete = complete && resolved
			if common == "" {
				continue
			}
			repo, known = byCommonDir[common]
			if !known {
				inspectCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
				items, err := inspectRepoWorktrees(inspectCtx, checkout)
				complete = complete && err == nil && inspectCtx.Err() == nil
				cancel()
				if err == nil && len(items) > 0 {
					root := canonicalPath(items[0].Path)
					repo = &worktreeRepo{Root: root, Name: filepath.Base(root), Worktrees: items}
				}
				byCommonDir[common] = repo
			}
			byCWD[cwd] = repo
		}
		if repo != nil && source.id != "" {
			if repo.SessionCount == 0 {
				repo.firstSessionID = source.id
			}
			repo.SessionCount++
		}
	}
	tempDir := canonicalPath(os.TempDir())
	managedWorktreesDir := ""
	if dataDir != "" {
		managedWorktreesDir = canonicalPath(filepath.Join(dataDir, "worktrees"))
	}
	repos := make([]worktreeRepo, 0, len(byCommonDir))
	for _, repo := range byCommonDir {
		if repo == nil {
			continue
		}
		if filterEphemeralTemp && len(repo.Worktrees) > 1 {
			filtered := make([]WorktreeInfo, 0, len(repo.Worktrees))
			for _, wt := range repo.Worktrees {
				if !wt.IsPrimary {
					wtPath := canonicalPath(wt.Path)
					inTempDir := isStrictChildPath(wtPath, tempDir)
					inManaged := managedWorktreesDir != "" && isStrictChildPath(wtPath, managedWorktreesDir)
					wtHasSession := false
					for _, scwd := range sessionCWDs {
						if sameOrSubPath(scwd, wtPath) {
							wtHasSession = true
							break
						}
					}
					if inTempDir && !inManaged && !wtHasSession {
						continue
					}
				}
				filtered = append(filtered, wt)
			}
			repo.Worktrees = filtered
		}
		repos = append(repos, *repo)
	}
	sort.Slice(repos, func(left, right int) bool {
		if repos[left].SessionCount != repos[right].SessionCount {
			return repos[left].SessionCount > repos[right].SessionCount
		}
		if repos[left].Name != repos[right].Name {
			return repos[left].Name < repos[right].Name
		}
		return repos[left].Root < repos[right].Root
	})
	return repos, complete
}

func worktreeRepoKey(ctx context.Context, cwd string, byCheckout map[string]string) (checkout, common string, complete bool) {
	if info, err := os.Stat(cwd); err != nil || !info.IsDir() {
		return "", "", err == nil || errors.Is(err, os.ErrNotExist)
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	checkout, err := gitToplevel(ctx, cwd)
	if err != nil {
		return "", "", ctx.Err() == nil
	}
	checkout = canonicalPath(checkout)
	if common, ok := byCheckout[checkout]; ok {
		return checkout, common, common != ""
	}
	out, err := runGit(ctx, checkout, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if dir := strings.TrimSuffix(out, "\n"); err == nil && filepath.IsAbs(dir) {
		common = canonicalPath(dir)
	}
	byCheckout[checkout] = common
	return checkout, common, common != ""
}

type createWorktreeRequest struct {
	Session string `json:"session"`
	CWD     string `json:"cwd"`
	Branch  string `json:"branch"`
	BaseRef string `json:"baseRef"`
}

type mergeWorktreeRequest struct {
	CWD          string `json:"cwd"`
	TargetBranch string `json:"targetBranch"`
}

type removeWorktreeRequest struct {
	Path  string `json:"path"`
	Force bool   `json:"force"`
}

// handleListWorktrees handles GET /git/worktrees?session=<id>&cwd=<dir>.
func (s *Server) handleListWorktrees(w http.ResponseWriter, r *http.Request) {
	cwd, ok := s.requestWorkbenchCWD(r)
	if !ok || cwd == "" {
		// Allow standalone ?cwd=<absDir> when no session query parameter is supplied
		// (e.g. Bot workspace or sidebar hydration before a tab attaches).
		if r.FormValue("session") == "" {
			if rawCWD := strings.TrimSpace(r.FormValue("cwd")); rawCWD != "" && filepath.IsAbs(rawCWD) {
				if info, err := os.Stat(rawCWD); err == nil && info.IsDir() {
					cwd = filepath.Clean(rawCWD)
					ok = true
				}
			}
		}
	}
	if !ok || cwd == "" {
		writeJSON(w, http.StatusOK, worktreeListResponse{
			Root:      "",
			NoCwd:     true,
			Worktrees: []WorktreeInfo{},
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), worktreeOpTimeout)
	defer cancel()

	root, err := gitToplevel(ctx, cwd)
	if err != nil {
		writeJSON(w, http.StatusOK, worktreeListResponse{
			Root:      "",
			NotGit:    true,
			Worktrees: []WorktreeInfo{},
		})
		return
	}

	items, err := inspectRepoWorktrees(ctx, root)
	if err != nil {
		writeJSON(w, http.StatusOK, worktreeListResponse{
			Root:      "",
			NotGit:    true,
			Worktrees: []WorktreeInfo{},
		})
		return
	}
	writeJSON(w, http.StatusOK, worktreeListResponse{
		Root:      root,
		Worktrees: items,
	})
}

// handleCreateWorktree handles POST /git/worktrees.
func (s *Server) handleCreateWorktree(w http.ResponseWriter, r *http.Request) {
	var req createWorktreeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	branch, slug, err := sanitizeGitBranchAndSlug(req.Branch)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	cwd, ok := s.resolveCreateWorktreeCWD(r, req.Session, req.CWD)
	if !ok || cwd == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "valid git repository cwd or session required"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), worktreeOpTimeout)
	defer cancel()

	root, err := gitToplevel(ctx, cwd)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "not a git repository"})
		return
	}

	// Anchor repository slug and git worktree operations to the primary checkout so creating
	// a new worktree while focused on an existing linked worktree still places siblings cleanly
	// under <dataDir>/worktrees/<repoBase>/<slug>.
	primaryRoot := root
	existing, _ := parseWorktreeList(ctx, root)
	if len(existing) > 0 && existing[0].Path != "" {
		primaryRoot = existing[0].Path
	}

	baseRef := strings.TrimSpace(req.BaseRef)
	if baseRef != "" {
		if err := validateGitRefArg(baseRef); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid baseRef: " + err.Error()})
			return
		}
	} else {
		baseRef = detectDefaultBaseRef(ctx, primaryRoot)
	}

	// Prune stale git worktree records whose directories were removed externally.
	_, _, _ = execGitWrite(ctx, primaryRoot, "worktree", "prune")
	existing, _ = parseWorktreeList(ctx, primaryRoot)
	for _, wt := range existing {
		if !wt.IsPrimary && wt.Branch == branch {
			if info, statErr := os.Stat(wt.Path); statErr == nil && info.IsDir() {
				// Idempotent return if this exact branch already has a live linked worktree.
				writeJSON(w, http.StatusCreated, map[string]any{
					"path":      wt.Path,
					"branch":    wt.Branch,
					"baseRef":   baseRef,
					"isPrimary": false,
				})
				return
			}
		}
	}

	repoSlug := sanitizeSlugToken(filepath.Base(primaryRoot))
	if repoSlug == "" {
		repoSlug = "repo"
	}
	repoWorktreesBase := filepath.Join(s.dataDir(), "worktrees", repoSlug)
	targetPath := filepath.Join(repoWorktreesBase, slug)

	// Guard against traversal and against rare directory collisions between two distinct repos
	// that happen to share the same directory basename.
	if !isStrictChildPath(targetPath, repoWorktreesBase) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid worktree target path"})
		return
	}
	if _, statErr := os.Stat(targetPath); statErr == nil {
		h := sha1.Sum([]byte(primaryRoot + "\x00" + branch))
		targetPath = filepath.Join(repoWorktreesBase, slug+"-"+hex.EncodeToString(h[:3]))
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to prepare worktrees directory"})
		return
	}

	// Attach existing local branch if it already exists; otherwise create a new branch off baseRef.
	if branchExistsLocal(ctx, primaryRoot, branch) {
		if _, stderr, addErr := execGitWrite(ctx, primaryRoot, "worktree", "add", "--", targetPath, branch); addErr != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": cleanGitErr(stderr, addErr)})
			return
		}
	} else {
		if _, stderr, addErr := execGitWrite(ctx, primaryRoot, "worktree", "add", "-b", branch, targetPath, baseRef); addErr != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": cleanGitErr(stderr, addErr)})
			return
		}
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"path":      filepath.Clean(targetPath),
		"branch":    branch,
		"baseRef":   baseRef,
		"isPrimary": false,
	})
}

// handleMergeWorktree handles POST /git/worktrees/merge.
func (s *Server) handleMergeWorktree(w http.ResponseWriter, r *http.Request) {
	var req mergeWorktreeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	rawCWD := strings.TrimSpace(req.CWD)
	if rawCWD == "" || !filepath.IsAbs(rawCWD) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "absolute cwd is required"})
		return
	}
	info, err := os.Stat(rawCWD)
	if err != nil || !info.IsDir() {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cwd directory does not exist"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), worktreeOpTimeout)
	defer cancel()

	wtRoot, err := gitToplevel(ctx, rawCWD)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "not a git repository"})
		return
	}

	worktrees, err := inspectRepoWorktrees(ctx, wtRoot)
	if err != nil || len(worktrees) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unable to inspect git worktrees"})
		return
	}

	primary := worktrees[0]
	var source *WorktreeInfo
	for i := range worktrees {
		if samePath(worktrees[i].Path, wtRoot) {
			source = &worktrees[i]
			break
		}
	}
	if source == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "worktree not found"})
		return
	}
	if source.IsPrimary {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "primary checkout cannot be merged into itself"})
		return
	}
	if source.Branch == "" || source.Branch == "HEAD" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "source worktree is on detached HEAD"})
		return
	}
	if source.Dirty {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "worktree has uncommitted changes",
			"code":  "dirty_worktree",
		})
		return
	}
	if primary.Dirty {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "primary checkout has uncommitted changes",
		})
		return
	}

	targetBranch := strings.TrimSpace(req.TargetBranch)
	if targetBranch != "" {
		if err := validateGitRefArg(targetBranch); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid targetBranch: " + err.Error()})
			return
		}
	} else if primary.Branch != "" && primary.Branch != "HEAD" {
		targetBranch = primary.Branch
	} else {
		targetBranch = "main"
	}

	if targetBranch == source.Branch {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "source and target branches are identical"})
		return
	}

	if primary.Branch != targetBranch {
		if s.hasActiveSessionInPath(primary.Path) {
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": "active session in primary checkout; cannot switch branches",
				"code":  "active_session",
			})
			return
		}
		if _, stderr, coErr := execGitWrite(ctx, primary.Path, "checkout", targetBranch); coErr != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": cleanGitErr(stderr, coErr)})
			return
		}
	}

	mergeMsg := fmt.Sprintf("Merge worktree branch '%s' into %s", source.Branch, targetBranch)
	if _, stderr, mergeErr := execGitCommitCapable(ctx, primary.Path, "merge", "--no-ff", "--no-edit", "-m", mergeMsg, "--", source.Branch); mergeErr != nil {
		_, _, _ = execGitWrite(ctx, primary.Path, "merge", "--abort")
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "merge conflict: " + cleanGitErr(stderr, mergeErr),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"mergedBranch": source.Branch,
		"targetBranch": targetBranch,
	})
}

// handleRemoveWorktree handles POST /git/worktrees/remove with 3 hard HTTP 409 guards:
//  1. primary_protected (unconditional — primary checkout can never be removed)
//  2. dirty_worktree    (when !force)
//  3. active_session    (when !force)
func (s *Server) handleRemoveWorktree(w http.ResponseWriter, r *http.Request) {
	var req removeWorktreeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	rawPath := strings.TrimSpace(req.Path)
	if rawPath == "" || !filepath.IsAbs(rawPath) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "absolute path is required"})
		return
	}
	cleanTarget := filepath.Clean(rawPath)
	info, err := os.Stat(cleanTarget)
	if err != nil || !info.IsDir() {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "worktree path does not exist"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), worktreeOpTimeout)
	defer cancel()

	// Guard 1 (fast structural check): in Git, the primary working tree owns a `.git` directory,
	// whereas linked worktrees created by `git worktree add` have a `.git` file pointing into
	// `<primary>/.git/worktrees/<name>`.
	if gitDotInfo, statErr := os.Stat(filepath.Join(cleanTarget, ".git")); statErr == nil && gitDotInfo.IsDir() {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "primary checkout cannot be removed",
			"code":  "primary_protected",
		})
		return
	}

	wtRoot, err := gitToplevel(ctx, cleanTarget)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "not a git worktree"})
		return
	}

	worktrees, err := inspectRepoWorktrees(ctx, wtRoot)
	if err != nil || len(worktrees) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unable to inspect git worktrees"})
		return
	}

	primary := worktrees[0]
	var target *WorktreeInfo
	for i := range worktrees {
		if samePath(worktrees[i].Path, cleanTarget) || samePath(worktrees[i].Path, wtRoot) {
			target = &worktrees[i]
			break
		}
	}
	if target == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "worktree not registered in repository"})
		return
	}

	// Hard Guard 1: primary checkout is unconditionally protected (even with force: true).
	if target.IsPrimary || samePath(target.Path, primary.Path) {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "primary checkout cannot be removed",
			"code":  "primary_protected",
		})
		return
	}

	// Hard Guard 2: uncommitted/untracked changes require explicit force: true.
	if !req.Force && target.Dirty {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "worktree has uncommitted changes",
			"code":  "dirty_worktree",
		})
		return
	}

	// Hard Guard 3: active session whose CWD is inside target.Path requires explicit force: true.
	if !req.Force && s.hasActiveSessionInPath(target.Path) {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "active session in worktree",
			"code":  "active_session",
		})
		return
	}

	args := []string{"worktree", "remove"}
	if req.Force {
		args = append(args, "--force")
	}
	args = append(args, "--", target.Path)
	if _, stderr, rmErr := execGitWrite(ctx, primary.Path, args...); rmErr != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": cleanGitErr(stderr, rmErr)})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) resolveCreateWorktreeCWD(r *http.Request, sessionID, cwdParam string) (string, bool) {
	if c := strings.TrimSpace(cwdParam); c != "" {
		if filepath.IsAbs(c) {
			if info, err := os.Stat(c); err == nil && info.IsDir() {
				return filepath.Clean(c), true
			}
		}
		return "", false
	}
	if sid := strings.TrimSpace(sessionID); sid != "" {
		if resolved, ok := s.workbenchCWD(r.Context(), sid, ""); ok && resolved != "" {
			return resolved, true
		}
		return "", false
	}
	if resolved, ok := s.requestWorkbenchCWD(r); ok && resolved != "" {
		return resolved, true
	}
	// Fallback when neither cwd nor session was supplied (e.g. Bot item spawn before tab focus):
	// prefer a running session's repository CWD, else server working directory if it is a git repo.
	if s != nil && s.mgr != nil {
		list := s.mgr.List()
		for i := len(list) - 1; i >= 0; i-- {
			sess := list[i]
			if sess == nil {
				continue
			}
			sess.mu.Lock()
			st, scwd := sess.Status, sess.CWD
			sess.mu.Unlock()
			if st == StatusRunning && scwd != "" {
				if _, err := gitToplevel(r.Context(), scwd); err == nil {
					return filepath.Clean(scwd), true
				}
			}
		}
	}
	if wd, err := os.Getwd(); err == nil && wd != "" {
		if _, gerr := gitToplevel(r.Context(), wd); gerr == nil {
			return filepath.Clean(wd), true
		}
	}
	return "", false
}

func (s *Server) hasActiveSessionInPath(wtPath string) bool {
	if s == nil || s.mgr == nil {
		return false
	}
	for _, sess := range s.mgr.List() {
		if sess == nil {
			continue
		}
		sess.mu.Lock()
		status := sess.Status
		sessCWD := sess.CWD
		sess.mu.Unlock()
		if status != StatusRunning {
			continue
		}
		if sessCWD != "" && sameOrSubPath(sessCWD, wtPath) {
			return true
		}
		if live := processCWD(sess.ShellPID()); live != "" && sameOrSubPath(live, wtPath) {
			return true
		}
	}
	return false
}

// inspectRepoWorktrees parses `git worktree list --porcelain` and enriches each worktree with
// dirty status, merged ancestry against the primary checkout, and bounded build-cache footprint.
func inspectRepoWorktrees(ctx context.Context, dir string) ([]WorktreeInfo, error) {
	items, err := parseWorktreeList(ctx, dir)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return []WorktreeInfo{}, nil
	}

	primaryHead := items[0].Head
	primaryPath := items[0].Path
	firstParents := gitFirstParentSet(ctx, primaryPath, primaryHead)

	for i := range items {
		wt := &items[i]
		info, statErr := os.Stat(wt.Path)
		if statErr != nil || !info.IsDir() {
			wt.statusUnreadable = true
			continue
		}
		out, err := runLocalWorktreeGit(ctx, wt.Path, "status", "--porcelain=v1")
		wt.Dirty, wt.statusUnreadable = strings.TrimSpace(out) != "" && err == nil, err != nil
		if !wt.IsPrimary {
			wt.CacheMB = estimateWorktreeCacheMB(wt.Path)
		}
		if !wt.IsPrimary && wt.Head != "" && primaryHead != "" && !isZeroGitOID(wt.Head) && !isZeroGitOID(primaryHead) {
			// A branch newly created off main sits on main's first-parent spine with 0 new commits
			// of its own — it must NOT show as "merged" before work has even started. Once merged
			// into primary (--no-ff), wt.Head is an ancestor of primaryHead AND is a second-parent
			// merge ancestor (not on primary's first-parent spine).
			if wt.Head != primaryHead && !firstParents[wt.Head] && isGitAncestor(ctx, primaryPath, wt.Head, primaryHead) {
				wt.Merged = true
			}
		}
	}
	return items, nil
}

// parseWorktreeList runs `git worktree list --porcelain` via slice args (`runGit`) and parses
// each stanza. Git guarantees the main worktree is emitted first.
func parseWorktreeList(ctx context.Context, dir string) ([]WorktreeInfo, error) {
	out, err := runGit(ctx, dir, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	return parseWorktreePorcelain(out), nil
}

func parseWorktreePorcelain(out string) []WorktreeInfo {
	blocks := strings.Split(strings.TrimSpace(out), "\n\n")
	var items []WorktreeInfo
	for _, block := range blocks {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		var (
			wtPath string
			head   string
			branch string
			isBare bool
		)
		for _, line := range strings.Split(block, "\n") {
			line = strings.TrimRight(line, "\r")
			switch {
			case strings.HasPrefix(line, "worktree "):
				wtPath = filepath.Clean(strings.TrimPrefix(line, "worktree "))
			case strings.HasPrefix(line, "HEAD "):
				head = strings.TrimSpace(strings.TrimPrefix(line, "HEAD "))
			case strings.HasPrefix(line, "branch "):
				ref := strings.TrimSpace(strings.TrimPrefix(line, "branch "))
				branch = strings.TrimPrefix(ref, "refs/heads/")
			case line == "detached":
				if branch == "" {
					branch = "HEAD"
				}
			case line == "bare":
				isBare = true
			}
		}
		if wtPath == "" || isBare {
			continue
		}
		if branch == "" {
			branch = "HEAD"
		}
		items = append(items, WorktreeInfo{
			Path:      wtPath,
			Branch:    branch,
			Head:      head,
			IsPrimary: len(items) == 0,
		})
	}
	return items
}

func runLocalWorktreeGit(ctx context.Context, dir string, args ...string) (string, error) {
	full := append([]string{"-C", dir, "--no-optional-locks", "-c", "core.fsmonitor=false", "-c", "core.quotepath=false"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = append(os.Environ(), "GIT_NO_LAZY_FETCH=1", "GIT_ALLOW_PROTOCOL=", "GIT_PAGER=cat", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	return string(out), err
}

func isGitAncestor(ctx context.Context, dir, ancestor, descendant string) bool {
	_, err := runLocalWorktreeGit(ctx, dir, "merge-base", "--is-ancestor", ancestor, descendant)
	return err == nil
}

func gitFirstParentSet(ctx context.Context, dir, head string) map[string]bool {
	res := make(map[string]bool)
	if dir == "" || head == "" || isZeroGitOID(head) {
		return res
	}
	out, err := runLocalWorktreeGit(ctx, dir, "rev-list", "--first-parent", "--max-count=512", head)
	if err != nil {
		return res
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if oid := strings.TrimSpace(line); oid != "" {
			res[oid] = true
		}
	}
	return res
}

func isZeroGitOID(oid string) bool {
	if oid == "" {
		return true
	}
	for i := 0; i < len(oid); i++ {
		if oid[i] != '0' {
			return false
		}
	}
	return true
}

func detectDefaultBaseRef(ctx context.Context, dir string) string {
	if sym, err := runGit(ctx, dir, "symbolic-ref", "--quiet", "--short", "HEAD"); err == nil {
		if b := strings.TrimSpace(sym); b != "" {
			return b
		}
	}
	if _, err := runGit(ctx, dir, "show-ref", "--verify", "--quiet", "refs/heads/main"); err == nil {
		return "main"
	}
	return "HEAD"
}

func branchExistsLocal(ctx context.Context, dir, branch string) bool {
	_, err := runGit(ctx, dir, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

// execGitWrite executes a mutating git subcommand with slice arguments ONLY (no shell, locks enabled).
func execGitWrite(ctx context.Context, dir string, args ...string) (stdout, stderr string, err error) {
	full := append([]string{"-C", dir, "-c", "core.quotepath=false"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = append(os.Environ(), "GIT_PAGER=cat", "GIT_TERMINAL_PROMPT=0")
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err = cmd.Run()
	return outBuf.String(), errBuf.String(), err
}

// execGitCommitCapable runs a git command that may create a commit (e.g. `merge --no-ff`).
// Preserves the user's configured git identity when present, falling back to a local identity
// on bare CI/container hosts without ~/.gitconfig so commit creation never fails spuriously.
func execGitCommitCapable(ctx context.Context, dir string, args ...string) (stdout, stderr string, err error) {
	prefix := []string{"-C", dir, "-c", "core.quotepath=false", "-c", "commit.gpgsign=false"}
	if email, cfgErr := runGit(ctx, dir, "config", "--get", "user.email"); cfgErr != nil || strings.TrimSpace(email) == "" {
		prefix = append(prefix, "-c", "user.email=deepwork@local", "-c", "user.name=DeepWork")
	}
	full := append(prefix, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = append(os.Environ(), "GIT_PAGER=cat", "GIT_TERMINAL_PROMPT=0")
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err = cmd.Run()
	return outBuf.String(), errBuf.String(), err
}

func cleanGitErr(stderr string, err error) string {
	if s := strings.TrimSpace(stderr); s != "" {
		return s
	}
	if err != nil {
		return err.Error()
	}
	return "git operation failed"
}

// sanitizeGitBranchAndSlug validates a client-supplied branch name against flag injection and
// git check-ref-format hazards, returning the clean branch name and a filesystem-safe directory slug.
func sanitizeGitBranchAndSlug(raw string) (branch string, slug string, err error) {
	b := strings.TrimSpace(raw)
	if err := validateGitRefArg(b); err != nil {
		return "", "", err
	}
	slug = sanitizeSlugToken(b)
	if slug == "" {
		return "", "", errors.New("branch produces empty directory slug")
	}
	return b, slug, nil
}

func validateGitRefArg(ref string) error {
	if ref == "" {
		return errors.New("branch/ref is required")
	}
	if len(ref) > 128 {
		return errors.New("branch/ref too long")
	}
	if strings.HasPrefix(ref, "-") {
		return errors.New("branch/ref must not start with '-'")
	}
	if strings.HasPrefix(ref, "/") || strings.HasSuffix(ref, "/") || strings.HasPrefix(ref, ".") || strings.HasSuffix(ref, ".") {
		return errors.New("invalid branch/ref boundary character")
	}
	if strings.Contains(ref, "..") || strings.Contains(ref, "//") || strings.Contains(ref, "@{") || strings.HasSuffix(ref, ".lock") {
		return errors.New("invalid branch/ref sequence")
	}
	for _, r := range ref {
		if r <= 0x20 || r == 0x7f || strings.ContainsRune(`~^:?*[\`, r) {
			return errors.New("branch/ref contains forbidden characters")
		}
	}
	return nil
}

func sanitizeSlugToken(s string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		isAlphaNum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if isAlphaNum || r == '_' {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	res := strings.Trim(b.String(), "-_")
	if len(res) > 64 {
		res = strings.Trim(res[:64], "-_")
	}
	return res
}

func canonicalPath(p string) string {
	cleaned := filepath.Clean(p)
	if resolved, err := filepath.EvalSymlinks(cleaned); err == nil {
		return filepath.Clean(resolved)
	}
	return cleaned
}

func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	return canonicalPath(a) == canonicalPath(b)
}

func sameOrSubPath(child, parent string) bool {
	if child == "" || parent == "" {
		return false
	}
	c := canonicalPath(child)
	p := canonicalPath(parent)
	if c == p {
		return true
	}
	return strings.HasPrefix(c, p+string(filepath.Separator))
}

func isStrictChildPath(target, baseDir string) bool {
	rel, err := filepath.Rel(filepath.Clean(baseDir), filepath.Clean(target))
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

var worktreeCacheDirs = []string{
	"node_modules",
	"frontend/node_modules",
	"target",
	".venv",
	"dist",
	".cache",
}

func estimateWorktreeCacheMB(wtPath string) int {
	const maxFilesVisited = 2000
	var totalBytes int64
	visited := 0
	for _, rel := range worktreeCacheDirs {
		dir := filepath.Join(wtPath, filepath.FromSlash(rel))
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() {
			continue
		}
		_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			visited++
			if visited > maxFilesVisited {
				return fs.SkipAll
			}
			if d.Type().IsRegular() {
				if fi, fiErr := d.Info(); fiErr == nil {
					totalBytes += fi.Size()
				}
			}
			return nil
		})
		if visited > maxFilesVisited {
			break
		}
	}
	return int(totalBytes / (1024 * 1024))
}
