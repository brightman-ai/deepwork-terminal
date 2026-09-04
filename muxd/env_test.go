package muxd

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests travelled here with the implementation. They are regression gates for a
// real incident (inherited agent-identity markers silently disabling transcript
// persistence), and two of them derive their inputs from the production list itself so
// that adding an entry cannot leave a hole in the coverage.

func TestPTYEnvForBrowserTerminal(t *testing.T) {
	got := PTYEnv([]string{
		"PATH=/bin",
		"TERM=dumb",
		"COLORTERM=old",
		"SHELL=/bin/zsh",
	})

	assert.Contains(t, got, "PATH=/bin")
	assert.Contains(t, got, "SHELL=/bin/zsh")
	assert.Contains(t, got, "TERM=xterm-256color")
	assert.Contains(t, got, "COLORTERM=truecolor")
	assert.NotContains(t, got, "TERM=dumb")
	assert.NotContains(t, got, "COLORTERM=old")
}

// 宿主自己从某个 agent CLI 的 session 里被拉起来过，它的进程环境就永久带着那个 session 的身份标记；
// 宿主常驻不重启，于是它开出的每个面板 → 每个 shell → 里面敲的每个 agent 都继承了这份标记。
// Claude Code 见到 CLAUDE_CODE_CHILD_SESSION 就认为自己是嵌套 session，**默认不落盘 transcript** ——
// 没有报错、没有提示，只是记录不见了。这个测试是那次事故的回归闸。
func TestPTYEnvStripsInheritedAgentSessionMarkers(t *testing.T) {
	// 输入**从生产名单动态生成**：早先这里手写了一部分 marker 却遍历整个名单去断言"输出里没有"，
	// 于是没被输入的那些天然通过 —— 名单里新加一条，测试照绿，正是假覆盖。
	input := []string{"PATH=/bin"}
	for _, marker := range agentSessionMarkers {
		input = append(input, marker+"=leaked-from-host")
	}
	require.Len(t, input, len(agentSessionMarkers)+1, "每一个生产 marker 都必须真的出现在输入里")

	got := PTYEnv(input)

	for _, marker := range agentSessionMarkers {
		for _, item := range got {
			assert.False(t, strings.HasPrefix(item, marker+"="),
				"%s 必须被摘掉：它回答的是「我是谁的孩子」，继承给面板里的 agent 一定是错的", marker)
		}
	}
	assert.Contains(t, got, "PATH=/bin", "只摘身份标记，别的一个不动")
}

// 名单是**逐条**列的，判据是「我是谁的孩子」vs「我该怎么工作」。这一条守的是判据本身 ——
// 第一版实现正好违反了自己写下的规则，把配置和执行边界事实也一起摘了。
func TestPTYEnvKeepsLookalikesThatAreNotLineage(t *testing.T) {
	keep := []string{
		// 配置：推理档位。摘掉等于替使用者悄悄改设置。
		"CLAUDE_EFFORT=high",
		// 执行边界的事实：如果 PTY 其实仍在同一个 OS 沙箱里（我们无从判断），摘掉只会让子 agent
		// 对自己有没有网络做出错误判断 —— 比继承更危险。
		"CODEX_SANDBOX=seatbelt",
		"CODEX_SANDBOX_NETWORK_DISABLED=1",
	}
	got := PTYEnv(append([]string{"PATH=/bin"}, keep...))
	for _, item := range keep {
		assert.Contains(t, got, item,
			"%s 名字看着同族，但它不是血缘标记 —— 判据一旦写下就得自己守住", item)
	}
}

// 名单是逐条列的而不是按 CLAUDE_CODE_* / CODEX_* 前缀一刀切 —— 因为同一个前缀下混着**配置**和
// **凭据**。把它们一起摘掉，面板里的 agent 会变得没配置、甚至登不上，而那同样是静默的。
func TestPTYEnvKeepsAgentConfigAndCredentials(t *testing.T) {
	keep := []string{
		"CLAUDE_CODE_MAX_OUTPUT_TOKENS=8192",         // 配置
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", // 配置
		"CODEX_HOME=/home/u/.codex",                  // 配置目录
		"CODEX_API_KEY=sk-test",                      // 凭据
		"CODEX_ACCESS_TOKEN=tok",                     // 凭据
		"ANTHROPIC_API_KEY=sk-ant-test",              // 凭据
	}
	got := PTYEnv(append([]string{"PATH=/bin"}, keep...))
	for _, item := range keep {
		assert.Contains(t, got, item, "这是配置或凭据，不是血缘标记，必须原样传给子进程")
	}
}

// TestPTYEnvStripsTheHostsTmuxIdentity: a host started inside a tmux pane carries $TMUX and
// $TMUX_PANE forever, and every PTY it opens used to inherit them.
//
// The visible failure is `tmux attach` inside a web terminal being refused as a nested
// client, which is a real, reported behaviour of this product and previously worked around by
// each caller remembering `env -u TMUX`. Both directions are asserted: the lineage goes, and
// TMUX_TMPDIR — which says where to find the socket rather than whose child you are — stays.
func TestPTYEnvStripsTheHostsTmuxIdentity(t *testing.T) {
	got := PTYEnv([]string{
		"TMUX=/tmp/tmux-1001/default,4006573,0",
		"TMUX_PANE=%2",
		"TMUX_TMPDIR=/tmp/mine",
		"PATH=/usr/bin",
	})
	for _, banned := range []string{"TMUX=", "TMUX_PANE="} {
		for _, item := range got {
			if strings.HasPrefix(item, banned) {
				t.Errorf("PTYEnv kept %q — a PTY inheriting the host's tmux identity gets "+
					"refused as a nested client when it runs `tmux attach`", item)
			}
		}
	}
	var keptTmpdir, keptPath bool
	for _, item := range got {
		if item == "TMUX_TMPDIR=/tmp/mine" {
			keptTmpdir = true
		}
		if item == "PATH=/usr/bin" {
			keptPath = true
		}
	}
	if !keptTmpdir {
		t.Error("PTYEnv dropped TMUX_TMPDIR — that is configuration (where the socket is), " +
			"not lineage; without it a child cannot find the server the user is actually using")
	}
	if !keptPath {
		t.Error("PTYEnv dropped an ordinary variable")
	}
}
