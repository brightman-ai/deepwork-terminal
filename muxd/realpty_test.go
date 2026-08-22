package muxd

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// spawnedForTest runs RealPTY and returns the command it built, cleaning up the process
// and the master fd. It exists so these tests inspect the ACTUAL production construction
// rather than a re-implementation of it.
func spawnedForTest(t *testing.T, opts SpawnOptions) []string {
	t.Helper()
	if opts.Argv == nil {
		opts.Argv = []string{"/bin/sh"}
	}
	if opts.Cols == 0 {
		opts.Cols, opts.Rows = defaultCols, defaultRows
	}
	ptmx, cmd, err := RealPTY(opts)
	require.NoError(t, err, "RealPTY")
	t.Cleanup(func() {
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
		if ptmx != nil {
			_ = ptmx.Close()
		}
	})
	return cmd.Env
}

// TestPTYEnvRealPTYKeepsPWDInSyncWithDir
//
// Go 只在 `Cmd.Env == nil` 时才按 `Cmd.Dir` 把 `PWD` 改写成新目录。一旦显式赋 Env，那层同步就
// 没了 —— shell 里的 `$PWD` 会停在宿主的启动目录上，于是提示符、启动脚本、和一切读 `$PWD` 的
// 工具都指着一个它并不在的目录。目录说谎，而且一声不吭。
//
// （这个测试原本盯的是 terminal 包的 newShellCmd。PTY 构造搬进 daemon 之后，它跟着被测对象
// 搬到了这里——不变量没变，只是现在只有一处实现需要守。）
func TestPTYEnvRealPTYKeepsPWDInSyncWithDir(t *testing.T) {
	dir := t.TempDir()
	env := spawnedForTest(t, SpawnOptions{Cwd: dir})

	var pwds []string
	for _, item := range env {
		if strings.HasPrefix(item, "PWD=") {
			pwds = append(pwds, strings.TrimPrefix(item, "PWD="))
		}
	}
	require.Len(t, pwds, 1, "环境里必须**正好一个** PWD，多一个就有人会读到另一个")
	assert.Equal(t, dir, pwds[0], "$PWD 必须等于进程真正的工作目录")
}

// TestPTYEnvRealPTYScrubsInheritedIdentity is the surviving half of what used to be
// "两条 PTY 路径共用同一份环境构建".
//
// The old test guarded against fixing only ONE of two PTY construction paths — the exact
// hazard shell_cmd.go's incident comment described ("bug 不挑路径"). That hazard is now
// structurally gone: there is one path, in the daemon, and this is it. What remains worth
// guarding is the invariant itself — a PTY child must never inherit the identity markers
// that silently disable an agent's transcript.
func TestPTYEnvRealPTYScrubsInheritedIdentity(t *testing.T) {
	t.Setenv("CLAUDE_CODE_CHILD_SESSION", "1")
	t.Setenv("CODEX_THREAD_ID", "t-123")
	t.Setenv("CODEX_HOME", "/home/u/.codex")

	env := spawnedForTest(t, SpawnOptions{Cwd: t.TempDir()})
	require.NotNil(t, env, "必须显式造环境，不能让宿主环境整个漏下去")

	for _, item := range env {
		assert.False(t, strings.HasPrefix(item, "CLAUDE_CODE_CHILD_SESSION="),
			"血缘标记必须摘掉，否则面板里的 claude 会以为自己是嵌套 session、静默不落盘 transcript")
		assert.False(t, strings.HasPrefix(item, "CODEX_THREAD_ID="), "同上")
	}
	assert.Contains(t, env, "CODEX_HOME=/home/u/.codex", "配置要留下")
	assert.Contains(t, env, "TERM=xterm-256color", "终端能力必须显式给出")
}

// TestPTYEnvRealPTYUsesExplicitEnvNotHostLeak pins the failure this whole family exists
// for: an unset Cmd.Env means the host's entire environment reaches the child untouched.
func TestPTYEnvRealPTYUsesExplicitEnvNotHostLeak(t *testing.T) {
	t.Setenv("CLAUDECODE", "1")
	env := spawnedForTest(t, SpawnOptions{Cwd: t.TempDir()})
	for _, item := range env {
		if item == "CLAUDECODE=1" {
			t.Fatal("host identity leaked into the PTY child — Cmd.Env was not built from PTYEnv")
		}
	}
	// And the caller-supplied base is honoured rather than ignored.
	custom := spawnedForTest(t, SpawnOptions{
		Cwd: t.TempDir(),
		Env: []string{"PATH=" + os.Getenv("PATH"), "DW_MARKER=kept"},
	})
	assert.Contains(t, custom, "DW_MARKER=kept", "显式传入的环境必须被采用")
}
