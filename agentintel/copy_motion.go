package agentintel

import (
	"context"
	"fmt"
	"strings"
)

// copyMotions is the allowlist of copy-mode scroll commands the UI may invoke.
// Restricting to known motions keeps an arbitrary string from ever reaching
// `tmux send-keys -X`.
var copyMotions = map[string]bool{
	"halfpage-up":   true,
	"halfpage-down": true,
	"page-up":       true,
	"page-down":     true,
}

// CopyMotion runs a tmux copy-mode scroll command DIRECTLY against the server,
// bypassing the PTY keystroke stream.
//
// Why not keystrokes: driving the motion through the byte stream (prefix `[` to
// enter copy-mode, then prefix `:` + `send-keys -X <motion>` via the command
// prompt) proved fragile and silently no-ops while in copy-mode — empirically the
// motion never applied. A raw key (vi C-u / emacs M-Up) instead depends on the
// live mode-keys AND the user not having rebound it. A server-side
// `send-keys -X <motion>` is identical for every config and always applies.
//
// `send-keys -X` requires the pane to already be in a mode, so copy-mode is
// entered first ONLY when the pane is not already in one — re-entering would
// reset the scroll position to the bottom and defeat a scroll-up.
func (s *TmuxStateService) CopyMotion(ctx context.Context, session, motion string) error {
	if !copyMotions[motion] {
		return fmt.Errorf("unknown copy motion %q", motion)
	}
	target := strings.TrimSpace(session)

	if !s.paneInMode(ctx, target) {
		_ = tmuxCommandContext(ctx, withTarget(target, "copy-mode")...).Run()
	}
	return tmuxCommandContext(ctx, withTarget(target, "send-keys", "-X", motion)...).Run()
}

// NewSession creates a fresh DETACHED tmux session and switches the client that shellPID
// is attached through onto it, so the user lands directly in the new session.
//
// Why server-side: driving `new-session` through the client's keystroke stream fails —
// the command prompt opens on `prefix :` but the rest of the burst arrives before the
// prompt is ready (same fragility as copy-motion), and `new-session` run inside a client
// where $TMUX is set refuses to nest. Running it here (tmuxCommandContext sanitizes $TMUX)
// avoids both: `-d` creates without attaching (no nesting), then switch-client moves the
// client. Returns the new session name. When the client can't be resolved (shell not
// attached) the session is still created and the ~1s topology poll surfaces it.
func (s *TmuxStateService) NewSession(ctx context.Context, shellPID int) (string, error) {
	out, err := tmuxCommandContext(ctx, "new-session", "-d", "-P", "-F", "#{session_name}").Output()
	if err != nil {
		return "", err
	}
	name := strings.TrimSpace(string(out))
	if name == "" {
		return "", fmt.Errorf("new-session produced no name")
	}
	if client := s.prober.FindClientName(ctx, shellPID); client != "" {
		_ = tmuxCommandContext(ctx, "switch-client", "-c", client, "-t", name).Run()
	}
	return name, nil
}

// RefreshClient forces tmux to fully redraw the screen to the client attached through
// shellPID. The web UI calls this to resync when xterm.js's grid has DIVERGED from tmux's
// model — ghosting: stale glyphs from a previous frame that survive in xterm's BUFFER (not just
// its renderer), so a client-side repaint can't clear them. tmux's incremental client update
// occasionally leaves such residue under a fullscreen TUI's differential redraws; a server-side
// `refresh-client` resends every cell and clears it (proven: a manual refresh-client fixes the
// residue while term.refresh does not). No size change → no reflow flicker.
func (s *TmuxStateService) RefreshClient(ctx context.Context, shellPID int) error {
	if client := s.prober.FindClientName(ctx, shellPID); client != "" {
		return tmuxCommandContext(ctx, "refresh-client", "-t", client).Run()
	}
	return tmuxCommandContext(ctx, "refresh-client").Run()
}

// paneInMode reports whether the target pane is currently in a mode (copy-mode etc.).
// A query failure reads as "not in mode" so the caller enters copy-mode defensively.
func (s *TmuxStateService) paneInMode(ctx context.Context, target string) bool {
	out, err := tmuxCommandContext(ctx, withTarget(target, "display-message", "-p", "#{pane_in_mode}")...).Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "1"
}

// withTarget builds a tmux argv: the command, an injected `-t <target>` when a
// target is given (empty → the active pane, no flag), then the rest.
func withTarget(target, cmd string, rest ...string) []string {
	args := []string{cmd}
	if target != "" {
		args = append(args, "-t", target)
	}
	return append(args, rest...)
}
