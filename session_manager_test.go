package terminal

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pipePTYFactory creates a pipe-based mock PTY for testing in environments
// where fork/exec is restricted. The read end acts as the "PTY master",
// and the write end is stored in the Cmd field's Stdout for simulation.
func pipePTYFactory(_ PTYStartOptions) (*os.File, *exec.Cmd, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	// We return the read end as the "master" (what readLoop reads from),
	// and store the write end so the test can write data to it.
	// Cmd is nil — no real process.
	// We store the write end in a way the test can access it.
	// For simplicity, we'll wrap it: the caller accesses sess.PTY for writes
	// but the read side is what gets passed as master.
	// Actually, let's return write-end as master for I/O (tests write to w,
	// readLoop reads from r). We need readLoop to read from r.
	// So master=r, and the test writes to w.
	// But we also need sess.PTY.Write to work for input simulation.
	// For the mock, we'll just use a pipe pair where:
	// - The returned master (r) is what readLoop reads from
	// - The write end (w) is stored somewhere the test can push data
	// Let's return r as the PTY master and store w in an accessible way.
	// We'll use the Cmd's ExtraFiles or a separate mechanism.

	// Simple approach: return the read end as PTY (readLoop reads from it).
	// The test gets the write end via the session's test helper.
	// But Cmd is nil so we need to handle that in Destroy gracefully.

	// We'll store w in a goroutine-safe way by using a custom approach.
	// For now, swap: master=w (so PTY.Write works for input simulation),
	// but readLoop reads from sess.PTY which would be w... that's wrong.
	// Let's use a different approach: return r as the pseudo-PTY,
	// and the test holds w to inject data.

	_ = w // w will be captured by the test via closure
	return r, nil, nil
}

// newTestManager creates a SessionManager with pipe-based mock PTY.
// Returns the manager and a helper to inject data into the most recently created session.
func newTestManager(t *testing.T) (*SessionManager, func(data []byte)) {
	t.Helper()

	var writeEnd *os.File

	factory := func(_ PTYStartOptions) (*os.File, *exec.Cmd, error) {
		r, w, err := os.Pipe()
		if err != nil {
			return nil, nil, err
		}
		writeEnd = w
		return r, nil, nil
	}

	sm := NewSessionManagerWithFactory(1024, "/bin/sh", factory)

	inject := func(data []byte) {
		if writeEnd != nil {
			writeEnd.Write(data)
		}
	}

	t.Cleanup(func() {
		sm.DestroyAll()
		if writeEnd != nil {
			writeEnd.Close()
		}
	})

	return sm, inject
}

// TC-08-SM-01: SessionManager.Create() creates a session with running status.
func TestSessionManager_Create(t *testing.T) {
	sm, _ := newTestManager(t)

	sess, err := sm.Create("test-session")
	require.NoError(t, err)
	require.NotNil(t, sess)

	assert.NotEmpty(t, sess.ID)
	assert.Equal(t, "test-session", sess.Name)
	assert.Equal(t, StatusRunning, sess.Status)
	assert.NotNil(t, sess.PTY)
	assert.NotNil(t, sess.Buffer)
	assert.False(t, sess.CreatedAt.IsZero())
	assert.False(t, sess.LastActive.IsZero())

	// Verify we can get it back.
	got, err := sm.Get(sess.ID)
	require.NoError(t, err)
	assert.Equal(t, sess.ID, got.ID)

	// Default name when empty.
	sess2, err := sm.Create("")
	require.NoError(t, err)
	// Default name uses MMdd-HHmm format (e.g. "0501-1020").
	assert.Regexp(t, `^\d{4}-\d{4}$`, sess2.Name)
}

func TestPTYEnvForBrowserTerminal(t *testing.T) {
	got := ptyEnv([]string{
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

	got := ptyEnv(input)

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
	got := ptyEnv(append([]string{"PATH=/bin"}, keep...))
	for _, item := range keep {
		assert.Contains(t, got, item,
			"%s 名字看着同族，但它不是血缘标记 —— 判据一旦写下就得自己守住", item)
	}
}

// Go 只在 `Cmd.Env == nil` 时才按 `Cmd.Dir` 把 `PWD` 改写成新目录。一旦显式赋 Env，那层同步就
// 没了 —— shell 里的 `$PWD` 会停在宿主的启动目录上，于是提示符、启动脚本、和一切读 `$PWD` 的
// 工具都指着一个它并不在的目录。和这轮在修的 cwd 问题是同一类：目录说谎，而且一声不吭。
func TestShellCmdKeepsPWDInSyncWithDir(t *testing.T) {
	dir := t.TempDir()
	cmd := newShellCmd(dir, "/bin/sh")

	var pwds []string
	for _, item := range cmd.Env {
		if strings.HasPrefix(item, "PWD=") {
			pwds = append(pwds, strings.TrimPrefix(item, "PWD="))
		}
	}
	require.Len(t, pwds, 1, "环境里必须**正好一个** PWD，多一个就有人会读到另一个")
	assert.Equal(t, dir, pwds[0], "$PWD 必须等于进程真正的工作目录")
}

// 名单是逐条列的而不是按 CLAUDE_CODE_* / CODEX_* 前缀一刀切 —— 因为同一个前缀下混着**配置**和
// **凭据**。把它们一起摘掉，面板里的 agent 会变得没配置、甚至登不上，而那同样是静默的。
func TestPTYEnvKeepsAgentConfigAndCredentials(t *testing.T) {
	keep := []string{
		"CLAUDE_CODE_MAX_OUTPUT_TOKENS=8192",          // 配置
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",  // 配置
		"CODEX_HOME=/home/u/.codex",                   // 配置目录
		"CODEX_API_KEY=sk-test",                       // 凭据
		"CODEX_ACCESS_TOKEN=tok",                      // 凭据
		"ANTHROPIC_API_KEY=sk-ant-test",               // 凭据
	}
	got := ptyEnv(append([]string{"PATH=/bin"}, keep...))
	for _, item := range keep {
		assert.Contains(t, got, item, "这是配置或凭据，不是血缘标记，必须原样传给子进程")
	}
}

// TC-08-SM-02: SessionManager.List() returns all sessions.
func TestSessionManager_List(t *testing.T) {
	sm, _ := newTestManager(t)

	// Empty list.
	list := sm.List()
	assert.Empty(t, list)

	// Create 3 sessions.
	for i := 0; i < 3; i++ {
		_, err := sm.Create("")
		require.NoError(t, err)
	}

	list = sm.List()
	assert.Len(t, list, 3)

	// All should have unique IDs.
	ids := make(map[string]bool)
	for _, s := range list {
		ids[s.ID] = true
	}
	assert.Len(t, ids, 3)
}

// TC-08-SM-03: SessionManager.Destroy() removes session and cleans up.
func TestSessionManager_Destroy(t *testing.T) {
	sm, _ := newTestManager(t)

	sess, err := sm.Create("to-destroy")
	require.NoError(t, err)

	err = sm.Destroy(sess.ID)
	require.NoError(t, err)

	// Session should be gone.
	_, err = sm.Get(sess.ID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")

	// List should be empty.
	assert.Empty(t, sm.List())

	// Destroying again should fail.
	err = sm.Destroy(sess.ID)
	assert.Error(t, err)
}

// TC-08-SM-04: Shell exit (pipe close) transitions status to "exited".
func TestSessionManager_ShellExitStatus(t *testing.T) {
	var writeEnd *os.File
	factory := func(_ PTYStartOptions) (*os.File, *exec.Cmd, error) {
		r, w, err := os.Pipe()
		if err != nil {
			return nil, nil, err
		}
		writeEnd = w
		return r, nil, nil
	}

	sm := NewSessionManagerWithFactory(1024, "/bin/sh", factory)

	sess, err := sm.Create("exit-test")
	require.NoError(t, err)

	// Close the write end to simulate shell exit (EOF on read end).
	writeEnd.Close()

	// Wait for the done channel (readLoop detects EOF).
	select {
	case <-sess.done:
		// Shell exited.
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for shell to exit")
	}

	sess.mu.Lock()
	status := sess.Status
	sess.mu.Unlock()
	assert.Equal(t, StatusExited, status, "session status should be 'exited' after pipe closes")
}

// 两条 PTY 路径共用同一份环境构建 —— 这个测试盯的是「只修了一条」这种漏法。
// newShellCmd（SessionV2 走的那条）此前根本没设 Env：宿主环境原样漏进 shell，连 TERM 都没有。
// 使用者走哪条路径取决于会话类型，而 bug 不挑路径。
func TestNewShellCmdUsesSamePTYEnv(t *testing.T) {
	t.Setenv("CLAUDE_CODE_CHILD_SESSION", "1")
	t.Setenv("CODEX_THREAD_ID", "t-123")
	t.Setenv("CODEX_HOME", "/home/u/.codex")

	cmd := newShellCmd(t.TempDir(), "/bin/sh")
	require.NotNil(t, cmd.Env, "必须显式造环境，不能让宿主环境整个漏下去")

	for _, item := range cmd.Env {
		assert.False(t, strings.HasPrefix(item, "CLAUDE_CODE_CHILD_SESSION="),
			"血缘标记必须摘掉，否则面板里的 claude 会以为自己是嵌套 session、静默不落盘 transcript")
		assert.False(t, strings.HasPrefix(item, "CODEX_THREAD_ID="), "同上")
	}
	assert.Contains(t, cmd.Env, "CODEX_HOME=/home/u/.codex", "配置要留下")
	assert.Contains(t, cmd.Env, "TERM=xterm-256color", "和另一条路径给出同样的终端能力")
}
