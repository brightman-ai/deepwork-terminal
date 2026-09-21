package agentintel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Named in-process teammates use teammate_spawned + agent_id, while their
// transcript filename uses a different ID. The runtime's sidecar supplies the
// exact (teamName, name) → transcript relation; never guess by newest file.
type pendingTeammate struct {
	row   map[string]any
	owner string
	depth int
}

func (t *claudeAgentTree) teammateTranscriptID(team, name string) string {
	if team == "" || name == "" {
		return ""
	}
	files, _ := filepath.Glob(filepath.Join(t.sessionDir, "subagents", "agent-*.meta.json"))
	found := ""
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var meta struct {
			Name string `json:"name"`
			Team string `json:"teamName"`
		}
		if json.Unmarshal(raw, &meta) != nil || meta.Name != name || meta.Team != team {
			continue
		}
		id := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), "agent-"), ".meta.json")
		if found != "" && found != id {
			return ""
		} // ambiguous ownership is not an identity
		found = id
	}
	return found
}

func (t *claudeAgentTree) agentRecipient(owner, recipient string) string {
	if t.nodes[recipient] != nil {
		return recipient
	}
	return t.aliases[agentAttemptKey(owner, recipient)]
}

func (t *claudeAgentTree) retryTeammates() {
	pending := t.pendingTeammates
	t.pendingTeammates = make(map[string]pendingTeammate)
	for _, p := range pending {
		t.scanUserRow(p.row, p.owner, p.depth)
	}
}
