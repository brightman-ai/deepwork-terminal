package terminal

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/brightman-ai/deepwork-terminal/muxd"
)

// styledSample is a realistic restore: a 200-column terminal holding 5000 lines of coloured log
// output in its scrollback (the depth the server restores, keyframeLines) and a screen of the same.
func styledSample(tb testing.TB) (muxd.TermState, []muxd.Line) {
	tb.Helper()
	h := muxd.NewTerminalModel(muxd.Grid{Cols: 200, Rows: 50}, 100_000)
	var b strings.Builder
	for i := 0; i < keyframeLines+50; i++ {
		fmt.Fprintf(&b, "\x1b[90m2026-09-05 12:00:00\x1b[0m \x1b[32mINFO\x1b[0m  worker[%05d] \x1b[1mfinished\x1b[0m in \x1b[33m12ms\x1b[0m "+
			"with payload %s and a trailing note that fills the rest of the line out to the edge\r\n", i, strings.Repeat("abcdef", 6))
	}
	h.Write([]byte(b.String()))
	st, scroll, ok := h.State(keyframeLines)
	if !ok {
		tb.Fatal("model broke")
	}
	return st, scroll
}

// The restore is sent on every connect and resync, so what it costs is a number to watch, not a
// feeling: bytes on the wire and time to produce them for the sample above.
func TestSerializeKeyframe_SizeOfAStyledSample(t *testing.T) {
	st, scroll := styledSample(t)
	var out []byte
	best := time.Duration(1 << 62)
	for i := 0; i < 5; i++ {
		start := time.Now()
		out = serializeKeyframe(st, scroll)
		best = min(best, time.Since(start))
	}
	t.Logf("keyframe: %d scrollback lines, %d bytes (%.1f B/line), serialize %v", len(scroll), len(out), float64(len(out))/float64(len(scroll)), best)
	// The sample's lines are about 200 columns of text plus a handful of colour changes: the keyframe
	// is those lines and little else (1,166,354 bytes when this was written).
	if limit := 1_250_000; len(out) > limit {
		t.Errorf("keyframe is %d bytes, over the %d budget: the restore is sent on every connect", len(out), limit)
	}
}

func BenchmarkSerializeKeyframe(b *testing.B) {
	st, scroll := styledSample(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = serializeKeyframe(st, scroll)
	}
}
