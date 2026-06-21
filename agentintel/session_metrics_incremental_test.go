package agentintel

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// The incremental accumulator (used by the cached overview path) must produce the SAME
// metrics whether a transcript is parsed in one shot or grown across multiple update() calls,
// and must reset correctly when the file is truncated/rotated.
func TestTranscriptAccum_IncrementalAndTruncation(t *testing.T) {
	rows := []string{
		`{"type":"user","userType":"external","timestamp":"2026-06-21T10:00:00Z","message":{"content":"first"}}`,
		`{"type":"assistant","timestamp":"2026-06-21T10:00:01Z","message":{"id":"m1","model":"x","usage":{"input_tokens":10,"output_tokens":5},"content":[{"type":"tool_use","name":"Bash"}]}}`,
		`{"type":"user","userType":"external","timestamp":"2026-06-21T10:01:00Z","message":{"content":"second"}}`,
		`{"type":"assistant","timestamp":"2026-06-21T10:01:01Z","message":{"id":"m2","model":"x","usage":{"input_tokens":20,"output_tokens":7}}}`,
	}
	join := func(n int) []byte {
		var b []byte
		for i := 0; i < n; i++ {
			b = append(b, rows[i]...)
			b = append(b, '\n')
		}
		return b
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "t.jsonl")

	// One-shot on the full file.
	require.NoError(t, os.WriteFile(p, join(len(rows)), 0o644))
	one := parseTranscript(p, "s", "", true)
	require.Equal(t, 2, one.Detail.TurnCount)
	require.Equal(t, 30, one.Summary.InputTokens)  // 10 + 20
	require.Equal(t, 12, one.Summary.OutputTokens) // 5 + 7

	// Incremental: write 2 rows, update; append the rest, update (delta) → equals one-shot.
	require.NoError(t, os.WriteFile(p, join(2), 0o644))
	a := newTranscriptAccum(p)
	a.update()
	require.NoError(t, os.WriteFile(p, join(len(rows)), 0o644)) // grew (append-equivalent)
	a.update()
	grown := a.snapshot("s", "", true)
	require.Equal(t, one.Detail.TurnCount, grown.Detail.TurnCount)
	require.Equal(t, one.Summary.InputTokens, grown.Summary.InputTokens)
	require.Equal(t, one.Summary.OutputTokens, grown.Summary.OutputTokens)

	// Truncation/rotation: file shrinks below the read offset → accumulator resets, no stale.
	require.NoError(t, os.WriteFile(p, join(2), 0o644))
	a.update()
	trunc := a.snapshot("s", "", true)
	require.Equal(t, 1, trunc.Detail.TurnCount)
	require.Equal(t, 10, trunc.Summary.InputTokens)
}
