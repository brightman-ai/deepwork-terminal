package terminal

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/brightman-ai/deepwork-terminal/agentintel"
	"github.com/google/uuid"
)

type clipboardTarget struct {
	ID          string `json:"id"`
	SessionID   string `json:"sessionId"`
	Name        string `json:"name"`
	PaneID      string `json:"paneId,omitempty"`
	PID         int    `json:"pid"`
	ShellPID    int    `json:"shellPid"`
	Active      bool   `json:"active"`
	ReadCommand string `json:"readCommand"`
}
type clipboardTmux interface {
	ClipboardPanes(context.Context, int) ([]agentintel.TmuxPane, bool, error)
	PasteClipboard(context.Context, int, string, int, string) error
}

func (s *Server) clipboardTargets(ctx context.Context, onlySession string) ([]clipboardTarget, error) {
	targets := make([]clipboardTarget, 0)
	for _, sess := range s.mgr.List() {
		if onlySession != "" && sess.ID != onlySession {
			continue
		}
		sess.mu.Lock()
		name, status := sess.Name, sess.Status
		sess.mu.Unlock()
		if status == StatusExited {
			continue
		}
		pid := sess.ShellPID()
		base := clipboardTarget{ID: sess.ID, SessionID: sess.ID, Name: name, PID: pid, ShellPID: pid, Active: true}
		if tm, ok := s.tmuxProvider.(clipboardTmux); ok {
			panes, attached, err := tm.ClipboardPanes(ctx, pid)
			if err != nil {
				return nil, err
			}
			if attached {
				for _, p := range panes {
					t := base
					t.ID += "/" + p.PaneID
					t.Name += fmt.Sprintf(" / %d · %s", p.WindowIndex, p.WindowName)
					if len(panes) > 1 {
						t.Name += " / " + p.PaneID
					}
					t.PaneID = p.PaneID
					t.PID = p.PanePID
					t.Active = p.Active && p.PaneActive
					targets = append(targets, t)
				}
				continue
			}
		}
		targets = append(targets, base)
	}
	for i := range targets {
		// Also works when embedded in a host whose executable has no clipboard CLI.
		targets[i].ReadCommand = "cat -- " + shellQuoteClipboard(ClipboardBufferPath(s.config.DataDir, targets[i].ID))
	}
	return targets, nil
}
func shellQuoteClipboard(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
func (s *Server) handleClipboardTargets(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	targets, err := s.clipboardTargets(ctx, "")
	if err != nil {
		writeJSON(w, 503, map[string]string{"error": "目标列表暂不可用，请重试"})
		return
	}
	host, _ := os.Hostname()
	writeJSON(w, 200, map[string]any{"targets": targets, "host": host})
}
func (s *Server) handleClipboardHistory(w http.ResponseWriter, r *http.Request) {
	since, _ := strconv.ParseUint(r.URL.Query().Get("since"), 10, 64)
	if r.URL.Query().Get("wait") == "1" {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		s.clipboard.wait(ctx, r.URL.Query().Get("epoch"), since)
		cancel()
	}
	epoch, cursor, entries := s.clipboard.snapshot(r.URL.Query().Get("epoch"), since)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"epoch": epoch, "cursor": cursor, "entries": entries})
}

type clipboardWrite struct {
	Target clipboardTarget `json:"target"`
	Text   string          `json:"text"`
}

func (s *Server) readClipboardWrite(w http.ResponseWriter, r *http.Request) (clipboardWrite, bool) {
	var body clipboardWrite
	r.Body = http.MaxBytesReader(w, r.Body, 6*clipboardMaxBytes+4096)
	if json.NewDecoder(r.Body).Decode(&body) != nil || body.Text == "" || len(body.Text) > clipboardMaxBytes || !utf8.ValidString(body.Text) {
		writeJSON(w, 400, map[string]string{"error": "请输入有效文本，最多 4 MiB"})
		return body, false
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	targets, err := s.clipboardTargets(ctx, body.Target.SessionID)
	if err == nil {
		for _, t := range targets {
			if t.ID == body.Target.ID && t.PID == body.Target.PID && t.ShellPID == body.Target.ShellPID {
				body.Target = t
				return body, true
			}
		}
	}
	writeJSON(w, 409, map[string]string{"error": "目标已断开或发生变化；草稿已保留，请重新选择目标"})
	return body, false
}
func (s *Server) handleClipboardSave(w http.ResponseWriter, r *http.Request) {
	body, ok := s.readClipboardWrite(w, r)
	if !ok {
		return
	}
	if err := writeClipboardBuffer(s.config.DataDir, body.Target.ID, body.Text); err != nil {
		writeJSON(w, 500, map[string]string{"error": "保存失败，请重试"})
		return
	}
	e := s.clipboard.add(clipboardEntry{ID: uuid.NewString(), SessionID: body.Target.SessionID, Source: body.Target.Name, TargetID: body.Target.ID, Text: body.Text, Direction: "local", Action: "已存到远端剪贴板"})
	writeJSON(w, 200, map[string]any{"entry": e, "target": body.Target})
}
func (s *Server) handleClipboardSend(w http.ResponseWriter, r *http.Request) {
	body, ok := s.readClipboardWrite(w, r)
	if !ok {
		return
	}
	// This endpoint is only called by the explicit second-stage "确认发送" action.
	// Never interpret escape/control sequences inside pasted text as terminal commands.
	for _, c := range body.Text {
		if (c < 32 && c != '\n' && c != '\r' && c != '\t') || c == 127 {
			writeJSON(w, 400, map[string]string{"error": "文本含终端控制字符，请先移除"})
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	var err error
	if body.Target.PaneID != "" {
		tm, ok := s.tmuxProvider.(clipboardTmux)
		if !ok {
			writeJSON(w, 501, map[string]string{"error": "此服务不支持向 pane 发送"})
			return
		}
		err = tm.PasteClipboard(ctx, body.Target.ShellPID, body.Target.PaneID, body.Target.PID, body.Text)
	} else {
		sess, getErr := s.mgr.Get(body.Target.SessionID)
		if getErr != nil {
			err = getErr
		} else {
			text := body.Text
			if sess.bracketedPaste.Load() {
				text = "\x1b[200~" + text + "\x1b[201~"
			}
			err = sess.WriteInput([]byte(text + "\r"))
		}
	}
	if err != nil {
		writeJSON(w, 502, map[string]string{"error": "发送未确认，请检查目标终端后再决定是否重试"})
		return
	}
	e := s.clipboard.add(clipboardEntry{ID: uuid.NewString(), SessionID: body.Target.SessionID, Source: body.Target.Name, TargetID: body.Target.ID, Text: body.Text, Direction: "local", Action: "已发送到目标终端"})
	writeJSON(w, 200, map[string]any{"ok": true, "entry": e})
}
