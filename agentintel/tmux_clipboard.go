package agentintel

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ClipboardPanes uses fresh topology for target validation. It never selects a
// window: addressing stable pane IDs keeps sends independent of viewer focus.
func (s *TmuxStateService) ClipboardPanes(ctx context.Context, shellPID int) ([]TmuxPane, bool, error) {
	if !s.prober.DetectTmux(ctx, shellPID) {
		return nil, false, nil
	}
	session := s.prober.FindClientSession(ctx, shellPID)
	if session == "" {
		return nil, true, fmt.Errorf("tmux client unavailable")
	}
	panes, err := s.prober.ListPanesForSession(ctx, session)
	return panes, true, err
}
func (s *TmuxStateService) PasteClipboard(ctx context.Context, shellPID int, paneID string, panePID int, text string) error {
	panes, attached, err := s.ClipboardPanes(ctx, shellPID)
	if err != nil {
		return err
	}
	if !attached {
		return fmt.Errorf("tmux client detached")
	}
	found := false
	for _, p := range panes {
		if p.PaneID == paneID && p.PanePID == panePID {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("target pane exited or changed")
	}
	name := "dw-clipboard-" + uuid.NewString()
	load := tmuxCommandContext(ctx, "load-buffer", "-b", name, "-")
	load.Stdin = strings.NewReader(text)
	if err := load.Run(); err != nil {
		return fmt.Errorf("cannot prepare clipboard: %w", err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = tmuxCommandContext(cleanup, "delete-buffer", "-b", name).Run()
	}()
	// One tmux command queue: paste then Enter always address the same stable pane.
	// Control mode returns a reply block per command, while prober.run consumes one.
	// A compound mutation must use one process with no automatic retry: retrying
	// after an ambiguous reply could execute the user's input a second time.
	return tmuxCommandContext(ctx, "paste-buffer", "-d", "-p", "-b", name, "-t", paneID, ";", "send-keys", "-t", paneID, "Enter").Run()
}
