package terminal

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/brightman-ai/deepwork-terminal/agentintel"
)

// REQ-cli-tabs-009 的钉。用户原话场景：「tab 1 的 tmux 里有 claude 在跑，但 tab 条看不到
// running 状态」——桥之前，attach 进 tmux 的 tab 在概览帧里永远 ToolNone（无点）。
//
// fakeTmuxStateProvider 复用 session_signal_test.go 里那份（typed state → 逐次 marshal），
// 于是这些用例同时钉住「typed TmuxState 经 JSON 线形往返后桥仍成立」——正是生产的真实路径。

func unitOf(status agentintel.AgentStatus) agentintel.SurfaceUnit {
	return agentintel.SurfaceUnit{AgentTool: agentintel.ToolClaude, AgentStatus: status}
}

func TestTmuxTabRollup_BackgroundRunningBeatsViewedIdle(t *testing.T) {
	// 用户场景本尊：客户端正看的窗口（active）是 idle，后台窗口在 running —— tab 点必须说 running。
	st := agentintel.TmuxState{
		Installed: true, ServerRunning: true, Attached: true, AttachedSession: "work",
		Sessions: []agentintel.TmuxSessionState{{
			Name: "work",
			Windows: []agentintel.TmuxWindowState{
				{Index: 1, Active: true, CWD: "/repo/a", SurfaceCard: agentintel.SurfaceCard{SurfaceUnit: unitOf(agentintel.StatusIdle)}},
				{Index: 2, Active: false, CWD: "/repo/b", SurfaceCard: agentintel.SurfaceCard{SurfaceUnit: unitOf(agentintel.StatusRunning)}},
			},
		}},
	}
	unit, cwd, ok := tmuxTabRollup(context.Background(), fakeTmuxStateProvider{state: st}, 1234)
	if !ok {
		t.Fatal("expected a bridged unit")
	}
	if unit.AgentStatus != agentintel.StatusRunning {
		t.Fatalf("background running must win over viewed idle, got %q", unit.AgentStatus)
	}
	if unit.AgentTool != agentintel.ToolClaude {
		t.Fatalf("tool = %q, want claude", unit.AgentTool)
	}
	if cwd != "/repo/a" {
		t.Fatalf("cwd should follow the ACTIVE window, got %q", cwd)
	}
}

func TestTmuxTabRollup_WaitingOutranksRunning(t *testing.T) {
	st := agentintel.TmuxState{
		Installed: true, Attached: true, AttachedSession: "work",
		Sessions: []agentintel.TmuxSessionState{{
			Name: "work",
			Windows: []agentintel.TmuxWindowState{
				{Index: 1, Active: true, SurfaceCard: agentintel.SurfaceCard{SurfaceUnit: unitOf(agentintel.StatusRunning)}},
				{Index: 2, SurfaceCard: agentintel.SurfaceCard{SurfaceUnit: unitOf(agentintel.StatusWaiting)}},
			},
		}},
	}
	unit, _, ok := tmuxTabRollup(context.Background(), fakeTmuxStateProvider{state: st}, 1234)
	if !ok || unit.AgentStatus != agentintel.StatusWaiting {
		t.Fatalf("waiting must outrank running, got ok=%v status=%q", ok, unit.AgentStatus)
	}
}

func TestTmuxTabRollup_AwaitingAggregatesAcrossWindows(t *testing.T) {
	since := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	waiting := unitOf(agentintel.StatusWaiting)
	waiting.AwaitingUser = true
	waiting.AwaitingSince = since
	st := agentintel.TmuxState{
		Installed: true, Attached: true, AttachedSession: "work",
		Sessions: []agentintel.TmuxSessionState{{
			Name: "work",
			Windows: []agentintel.TmuxWindowState{
				{Index: 1, Active: true, SurfaceCard: agentintel.SurfaceCard{SurfaceUnit: unitOf(agentintel.StatusIdle)}},
				{Index: 2, SurfaceCard: agentintel.SurfaceCard{SurfaceUnit: waiting}},
			},
		}},
	}
	unit, _, ok := tmuxTabRollup(context.Background(), fakeTmuxStateProvider{state: st}, 1234)
	if !ok || !unit.AwaitingUser || !unit.AwaitingSince.Equal(since) {
		t.Fatalf("awaiting must aggregate: %+v", unit)
	}
}

func TestTmuxTabRollup_AllIdleYieldsIdle(t *testing.T) {
	st := agentintel.TmuxState{
		Installed: true, Attached: true, AttachedSession: "work",
		Sessions: []agentintel.TmuxSessionState{{
			Name: "work",
			Windows: []agentintel.TmuxWindowState{
				{Index: 1, Active: true, SurfaceCard: agentintel.SurfaceCard{SurfaceUnit: unitOf(agentintel.StatusIdle)}},
			},
		}},
	}
	unit, _, ok := tmuxTabRollup(context.Background(), fakeTmuxStateProvider{state: st}, 1234)
	if !ok || unit.AgentStatus != agentintel.StatusIdle {
		t.Fatalf("all-idle session should still report (idle dot, same as a non-tmux idle agent), got ok=%v status=%q", ok, unit.AgentStatus)
	}
}

func TestTmuxTabRollup_NoAgentAnywhereMeansNoDot(t *testing.T) {
	st := agentintel.TmuxState{
		Installed: true, Attached: true, AttachedSession: "work",
		Sessions: []agentintel.TmuxSessionState{{
			Name:    "work",
			Windows: []agentintel.TmuxWindowState{{Index: 1, Active: true}},
		}},
	}
	if _, _, ok := tmuxTabRollup(context.Background(), fakeTmuxStateProvider{state: st}, 1234); ok {
		t.Fatal("a session of bare shells must not light the dot")
	}
}

func TestTmuxTabRollup_NotAttachedOrMissing(t *testing.T) {
	ok := func(st agentintel.TmuxState) bool {
		_, _, ok := tmuxTabRollup(context.Background(), fakeTmuxStateProvider{state: st}, 1234)
		return ok
	}
	if ok(agentintel.TmuxState{Installed: true, Attached: false}) {
		t.Fatal("not attached → no bridge")
	}
	// attach 与拓扑快照之间的竞态：AttachedSession 已不在 Sessions 里（session 刚死）。
	if ok(agentintel.TmuxState{Installed: true, Attached: true, AttachedSession: "ghost"}) {
		t.Fatal("attached session missing from topology → no bridge")
	}
	if _, _, ok := tmuxTabRollup(context.Background(), fakeTmuxStateProvider{err: errors.New("boom")}, 1234); ok {
		t.Fatal("provider error → no bridge")
	}
	if _, _, ok := tmuxTabRollup(context.Background(), nil, 1234); ok {
		t.Fatal("nil provider → no bridge")
	}
	if _, _, ok := tmuxTabRollup(context.Background(), fakeTmuxStateProvider{}, 0); ok {
		t.Fatal("no shellPID → no bridge")
	}
}

// 防御分支：host 注入的 provider 违反「JSON-encoded TmuxState」契约时静默无桥，而不是带着
// 半个解析结果点亮状态点。共享 fake 只会产出合法 JSON，这条需要自己的 raw 桩。
type rawTmuxStateProvider struct{ raw json.RawMessage }

func (r rawTmuxStateProvider) TmuxState(context.Context, int) (json.RawMessage, error) {
	return r.raw, nil
}

func TestTmuxTabRollup_BadJSON(t *testing.T) {
	if _, _, ok := tmuxTabRollup(context.Background(), rawTmuxStateProvider{raw: json.RawMessage("{")}, 1234); ok {
		t.Fatal("bad JSON → no bridge")
	}
}
