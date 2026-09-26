package terminal

import (
	"context"
	"encoding/json"

	"github.com/brightman-ai/deepwork-terminal/agentintel"
)

// tmuxTabRollup — REQ-cli-tabs-009 的桥：回答「一个手动 `tmux attach` 进某 session 的 dw tab，
// tab 条状态点该说什么」。
//
// 为什么需要它：tab 级 agent 状态的正路（sessionAgent.State）是进程树检测 —— shell 的后代里找
// claude/codex。但 attach 场景下 shell 的后代只有一个 tmux **client**，agent 隔在 tmux server 边界
// 另一边，进程树永远够不着（2026-09-26 实报：tab attach 的 session 里 claude 正在跑，tab 条无点）。
// 而 tmux_state 的 per-pane 检测一直知道真相 —— 这座桥只是把已知事实接过来。
//
// 语义（Human 2026-09-26 拍板，decisions.md）：**全 session roll-up**，不是「客户端正在看的窗口」
// ——tab 条的独有价值是「不切进去也知道」。规则零新造：复用 agentintel.RollUp（waiting > running >
// 当前窗口 tiebreak），与 tmux 窗口卡/概览逐字同一函数。
//
// 返回 ok=false 的情形：没 attach / provider 缺失或出错 / 拓扑里找不到那个 session（attach 与拓扑
// 快照之间的天然竞态）/ 整个 session 一个 agent 都没有（裸 shell 窗口群 → 无点，和裸 shell tab 一致）。
// unit.CWD 之外另回 active 窗口的 cwd，供调用方更新卡片的目录行（窗口卡先例：CWD 取 active）。
func tmuxTabRollup(ctx context.Context, provider TmuxStateProvider, shellPID int) (agentintel.SurfaceUnit, string, bool) {
	if provider == nil || shellPID <= 0 {
		return agentintel.SurfaceUnit{}, "", false
	}
	raw, err := provider.TmuxState(ctx, shellPID)
	if err != nil {
		return agentintel.SurfaceUnit{}, "", false
	}
	var st agentintel.TmuxState
	if json.Unmarshal(raw, &st) != nil || !st.Attached || st.AttachedSession == "" {
		return agentintel.SurfaceUnit{}, "", false
	}
	for i := range st.Sessions {
		ts := &st.Sessions[i]
		if ts.Name != st.AttachedSession {
			continue
		}
		units := make([]agentintel.SurfaceUnit, len(ts.Windows))
		active := -1
		for j := range ts.Windows {
			units[j] = ts.Windows[j].SurfaceUnit
			if ts.Windows[j].Active && active < 0 {
				active = j
			}
		}
		unit := agentintel.RollUp(units, active)
		if unit.AgentTool == agentintel.ToolNone {
			return agentintel.SurfaceUnit{}, "", false
		}
		cwd := ""
		if active >= 0 {
			cwd = ts.Windows[active].CWD
		}
		return unit, cwd, true
	}
	return agentintel.SurfaceUnit{}, "", false
}
