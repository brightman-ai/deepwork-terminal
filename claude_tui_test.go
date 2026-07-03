package terminal

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHandleClaudeTuiClassic_SetsClassicPreservingOtherKeys verifies the persist endpoint flips
// tui→classic in ~/.claude/settings.json while preserving every other key verbatim. Uses an
// isolated HOME so the real user config is never touched.
func TestHandleClaudeTuiClassic_SetsClassicPreservingOtherKeys(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	claudeDir := filepath.Join(home, ".claude")
	require.NoError(t, os.MkdirAll(claudeDir, 0o755))
	settingsPath := filepath.Join(claudeDir, "settings.json")
	// Pre-seed with fullscreen + an unrelated key that must survive.
	require.NoError(t, os.WriteFile(settingsPath,
		[]byte(`{"tui":"fullscreen","model":"opus","permissions":{"allow":["Bash"]}}`), 0o644))

	srv := &Server{}
	w := httptest.NewRecorder()
	srv.handleClaudeTuiClassic(w, httptest.NewRequest(http.MethodPost, "/claude/tui-classic", nil))
	assert.Equal(t, http.StatusOK, w.Code)

	data, err := os.ReadFile(settingsPath)
	require.NoError(t, err)
	var got map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &got))
	assert.JSONEq(t, `"classic"`, string(got["tui"]), "tui must be flipped to classic")
	assert.JSONEq(t, `"opus"`, string(got["model"]), "unrelated keys must be preserved")
	assert.JSONEq(t, `{"allow":["Bash"]}`, string(got["permissions"]), "nested keys must be preserved verbatim")
}

// TestHandleClaudeTuiClassic_CreatesFileWhenAbsent verifies a fresh settings.json is created when
// none exists.
func TestHandleClaudeTuiClassic_CreatesFileWhenAbsent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	srv := &Server{}
	w := httptest.NewRecorder()
	srv.handleClaudeTuiClassic(w, httptest.NewRequest(http.MethodPost, "/claude/tui-classic", nil))
	assert.Equal(t, http.StatusOK, w.Code)

	data, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	require.NoError(t, err)
	var got map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &got))
	assert.JSONEq(t, `"classic"`, string(got["tui"]))
}

// TestHandleClaudeTuiClassic_RefusesNonObject leaves a malformed settings file untouched rather than
// clobbering it.
func TestHandleClaudeTuiClassic_RefusesNonObject(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	claudeDir := filepath.Join(home, ".claude")
	require.NoError(t, os.MkdirAll(claudeDir, 0o755))
	settingsPath := filepath.Join(claudeDir, "settings.json")
	original := []byte("not json at all")
	require.NoError(t, os.WriteFile(settingsPath, original, 0o644))

	srv := &Server{}
	w := httptest.NewRecorder()
	srv.handleClaudeTuiClassic(w, httptest.NewRequest(http.MethodPost, "/claude/tui-classic", nil))
	assert.Equal(t, http.StatusConflict, w.Code)

	data, err := os.ReadFile(settingsPath)
	require.NoError(t, err)
	assert.Equal(t, original, data, "malformed file must be left untouched")
}
