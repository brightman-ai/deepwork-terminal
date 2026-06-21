package agentintel

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReadTailFunc(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.jsonl")
	var b strings.Builder
	for i := 0; i < 5; i++ {
		b.WriteString(fmt.Sprintf("{\"n\":%d}\n", i))
	}
	require.NoError(t, os.WriteFile(path, []byte(b.String()), 0o644))

	// Large window → reads every row, in order.
	var all []float64
	require.NoError(t, NewJSONLReader(path).ReadTailFunc(1<<20, func(r map[string]any) bool {
		all = append(all, r["n"].(float64))
		return true
	}))
	require.Equal(t, []float64{0, 1, 2, 3, 4}, all)

	// Tiny window → a suffix only; the partial leading line is dropped, newest row kept.
	var tail []float64
	require.NoError(t, NewJSONLReader(path).ReadTailFunc(12, func(r map[string]any) bool {
		tail = append(tail, r["n"].(float64))
		return true
	}))
	require.NotEmpty(t, tail)
	require.Less(t, len(tail), 5)
	require.Equal(t, float64(4), tail[len(tail)-1])

	// Early stop when fn returns false.
	cnt := 0
	require.NoError(t, NewJSONLReader(path).ReadTailFunc(1<<20, func(map[string]any) bool {
		cnt++
		return cnt < 2
	}))
	require.Equal(t, 2, cnt)
}
