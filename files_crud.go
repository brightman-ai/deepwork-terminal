package terminal

import (
	"net/http"
	"os"
	"path/filepath"
)

// This file adds the write-side of the file workbench (CHG-016 companion): mkdir /
// create / rename / delete. All four are POST, read session/cwd/operands from POST
// FORM values (not JSON body, not query — r.FormValue so either encoding works), and
// reuse the SAME path-safety primitives as the read side in files.go:
//   - s.workbenchCWD resolves the session's live cwd (tmux-active pane → /proc → session).
//   - safeResolve anchors a client-supplied relative path under that cwd and rejects
//     `..` traversal, absolute escapes, and symlink-outs with 403 — never silently
//     clamped into the tree.
//
// Every response goes through writeJSON, which always calls w.WriteHeader explicitly —
// no bare w.Write — so these can never fall through to a framework's default (the gin
// NoRoute 404-with-200-body class of bug this repo hit once already, see files.go's
// header comment on the drawer generally trusting an explicit status).

// handleFilesMkdir handles POST /files/mkdir. Form: session, cwd, path (new dir, rel
// to cwd). Creates exactly ONE new directory level (os.Mkdir, not MkdirAll) — a
// missing parent is a real error (400), not silently created.
func (s *Server) handleFilesMkdir(w http.ResponseWriter, r *http.Request) {
	cwd, ok := s.workbenchCWD(r.Context(), r.FormValue("session"), r.FormValue("cwd"))
	if !ok || cwd == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	rel := r.FormValue("path")
	if rel == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path required"})
		return
	}
	target, err := safeResolve(cwd, rel)
	if err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "path not allowed"})
		return
	}
	if err := os.Mkdir(target, 0o755); err != nil {
		if os.IsExist(err) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "already exists"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleFilesCreate handles POST /files/create. Form: session, cwd, path (new empty
// file, rel to cwd). Uses O_CREATE|O_EXCL so an existing path 409s instead of being
// silently truncated.
func (s *Server) handleFilesCreate(w http.ResponseWriter, r *http.Request) {
	cwd, ok := s.workbenchCWD(r.Context(), r.FormValue("session"), r.FormValue("cwd"))
	if !ok || cwd == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	rel := r.FormValue("path")
	if rel == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path required"})
		return
	}
	target, err := safeResolve(cwd, rel)
	if err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "path not allowed"})
		return
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if os.IsExist(err) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "already exists"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	_ = f.Close()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleFilesRename handles POST /files/rename. Form: session, cwd, from (rel), to
// (rel). from and to are EACH independently safeResolve'd against cwd — from must
// exist (404 if not), to must NOT exist (409 if it does), then os.Rename moves it.
func (s *Server) handleFilesRename(w http.ResponseWriter, r *http.Request) {
	cwd, ok := s.workbenchCWD(r.Context(), r.FormValue("session"), r.FormValue("cwd"))
	if !ok || cwd == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	from := r.FormValue("from")
	to := r.FormValue("to")
	if from == "" || to == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "from/to required"})
		return
	}
	fromTarget, err := safeResolve(cwd, from)
	if err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "path not allowed"})
		return
	}
	toTarget, err := safeResolve(cwd, to)
	if err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "path not allowed"})
		return
	}
	if _, err := os.Lstat(fromTarget); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if _, err := os.Lstat(toTarget); err == nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "already exists"})
		return
	}
	if err := os.Rename(fromTarget, toTarget); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleFilesDelete handles POST /files/delete. Form: session, cwd, path (rel).
// os.RemoveAll — a directory is removed recursively (the frontend confirms before
// sending this). The cwd root itself is never deletable: ANY spelling that resolves
// to the cwd root (empty, ".", "/", "./", …) 400s, checked on the RESOLVED path (not
// the raw string) so it can't be spelled around. An outright `..` escape attempt is
// already rejected by safeResolve with 403, same as every other /files/* handler.
func (s *Server) handleFilesDelete(w http.ResponseWriter, r *http.Request) {
	cwd, ok := s.workbenchCWD(r.Context(), r.FormValue("session"), r.FormValue("cwd"))
	if !ok || cwd == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	rel := r.FormValue("path")
	target, err := safeResolve(cwd, rel)
	if err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "path not allowed"})
		return
	}
	realCWD, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cwd not resolvable"})
		return
	}
	if target == realCWD {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cannot delete cwd root"})
		return
	}
	if _, err := os.Lstat(target); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if err := os.RemoveAll(target); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
