package muxd

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"testing"
)

// TEMPORARY: steps one vector through xterm and the model, reporting the first op whose result
// differs. DELETE BEFORE COMMIT.
func TestZZDebugVector(t *testing.T) {
	file, name := os.Getenv("XTERM_VECTORS"), os.Getenv("VEC")
	if name == "" {
		t.Skip()
	}
	data, _ := os.ReadFile(file)
	var vectors []xtermVector
	_ = json.Unmarshal(data, &vectors)
	out, err := exec.Command("node", "/tmp/xrepro/step.mjs", file, name).Output()
	if err != nil {
		t.Fatal(err)
	}
	var steps []struct {
		Cursor [2]int
		Alt    bool
		Rows   []string
	}
	_ = json.Unmarshal(out, &steps)
	for _, v := range vectors {
		if v.Name != name {
			continue
		}
		h := NewTerminalModel(Grid{Cols: v.Cols, Rows: v.Rows}, 100_000)
		var prev []string
		for i, op := range v.Ops {
			desc := ""
			if len(op.Resize) == 2 {
				h.vt.resize(op.Resize[1], op.Resize[0])
				desc = fmt.Sprintf("resize %v", op.Resize)
			} else {
				b, _ := base64.StdEncoding.DecodeString(op.W)
				h.vt.Write(b)
				desc = fmt.Sprintf("write %q", b)
			}
			st, _, _ := h.State(0)
			var rows []string
			for _, l := range modelLines(st.Lines) {
				p := " "
				if l.W {
					p = "+"
				}
				rows = append(rows, p+l.T)
			}
			want := steps[i]
			same := fmt.Sprint(rows) == fmt.Sprint(want.Rows) && [2]int{st.CursorCol, st.CursorRow} == want.Cursor && st.Alt == want.Alt
			if !same {
				t.Logf("FIRST DIVERGENCE at op %d: %s", i, desc)
				t.Logf("before:\n%s", join(prev))
				t.Logf("xterm  cursor=%v alt=%v:\n%s", want.Cursor, want.Alt, join(want.Rows))
				t.Logf("model  cursor=(%d,%d) alt=%v:\n%s", st.CursorCol, st.CursorRow, st.Alt, join(rows))
				return
			}
			prev = rows
		}
		t.Logf("no divergence")
	}
}

func join(rows []string) string {
	s := ""
	for i, r := range rows {
		s += fmt.Sprintf("  %2d|%s|\n", i, r)
	}
	return s
}
