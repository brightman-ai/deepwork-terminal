package agentintel

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ProjectLocator maps CWD to Claude/Codex project directories.
type ProjectLocator struct{}

func NewProjectLocator() *ProjectLocator {
	return &ProjectLocator{}
}

// ClaudeProjectDir returns the Claude JSONL directory for a project path.
// Claude Code encodes the project path by replacing '/' and '.' with '-'.
// Example: /Users/foo/proj → ~/.claude/projects/-Users-foo-proj/
// Important: resolves symlinks first (macOS /tmp → /private/tmp).
func (pl *ProjectLocator) ClaudeProjectDir(projectPath string) string {
	// Resolve symlinks: Claude Code uses the real path.
	if resolved, err := filepath.EvalSymlinks(projectPath); err == nil {
		projectPath = resolved
	}
	encoded := strings.NewReplacer("/", "-", ".", "-").Replace(projectPath)
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "projects", encoded)
}

// ClaudeSessionFiles returns all .jsonl files in the project directory,
// sorted by modification time (newest first).
func (pl *ProjectLocator) ClaudeSessionFiles(projectPath string) ([]string, error) {
	dir := pl.ClaudeProjectDir(projectPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	type fileInfo struct {
		path    string
		modTime int64
	}
	var files []fileInfo
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, fileInfo{
			path:    filepath.Join(dir, e.Name()),
			modTime: info.ModTime().UnixNano(),
		})
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime > files[j].modTime
	})

	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.path
	}
	return paths, nil
}

// ClaudeProjectsRoot returns ~/.claude/projects.
func (pl *ProjectLocator) ClaudeProjectsRoot() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "projects")
}

// ClaudeAllSessionFiles returns every .jsonl across ALL Claude projects, sorted
// newest-first by modification time. Used by the cross-project input drawer.
// Missing root → empty slice.
func (pl *ProjectLocator) ClaudeAllSessionFiles() []string {
	root := pl.ClaudeProjectsRoot()
	type fileInfo struct {
		path    string
		modTime int64
	}
	var files []fileInfo
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		files = append(files, fileInfo{path: path, modTime: info.ModTime().UnixNano()})
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].modTime > files[j].modTime })
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.path
	}
	return paths
}

// CodexSessionDir returns the Codex sessions base directory.
func (pl *ProjectLocator) CodexSessionDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex", "sessions")
}

// CodexSessionFiles returns every Codex rollout JSONL under ~/.codex/sessions,
// sorted newest-first by modification time. Codex nests rollouts by date
// (sessions/YYYY/MM/DD/rollout-*.jsonl), so a recursive walk is required — a
// flat ReadDir of the base dir finds nothing. Missing base dir → empty slice.
func (pl *ProjectLocator) CodexSessionFiles() []string {
	base := pl.CodexSessionDir()
	type fileInfo struct {
		path    string
		modTime int64
	}
	var files []fileInfo
	_ = filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable subtrees, keep walking
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		files = append(files, fileInfo{path: path, modTime: info.ModTime().UnixNano()})
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].modTime > files[j].modTime })
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.path
	}
	return paths
}

// CodexLatestSession finds the most recent Codex rollout JSONL for a project.
// Falls back to scanning ~/.codex/sessions/ by modification time when sqlite is unavailable.
func (pl *ProjectLocator) CodexLatestSession(projectPath string) (string, error) {
	sessionDir := pl.CodexSessionDir()
	entries, err := os.ReadDir(sessionDir)
	if err != nil {
		return "", err
	}

	type fileInfo struct {
		path    string
		modTime int64
	}
	var files []fileInfo
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, fileInfo{
			path:    filepath.Join(sessionDir, e.Name()),
			modTime: info.ModTime().UnixNano(),
		})
	}

	if len(files) == 0 {
		return "", os.ErrNotExist
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime > files[j].modTime
	})
	return files[0].path, nil
}
