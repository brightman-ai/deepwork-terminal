// Package terminal — shell_cmd.go: 跨平台 shell 命令构造辅助
package terminal

import (
	"os/exec"
	"strings"
	"syscall"
)

// spawnedCmd 包装 exec.Cmd，提供统一 Wait 接口
type spawnedCmd struct {
	cmd *exec.Cmd
}

// defaultShellCmd 默认 shell 路径（含参数），当 config 未指定时使用
const defaultShellCmd = "/bin/bash --login"

// newShellCmd 创建 shell 命令，cwd = rootDir。
// shellCmd 为完整命令字符串（如 "/bin/bash --login" 或 "/bin/zsh"）；
// 若为空则使用默认值 "/bin/bash --login"。
// 使用 Setpgid 隔离进程组（DDC-01 SIGHUP 隔离）
func newShellCmd(rootDir string, shellCmd string) *exec.Cmd {
	if shellCmd == "" {
		shellCmd = defaultShellCmd
	}
	parts := strings.Fields(shellCmd)
	var cmd *exec.Cmd
	if len(parts) == 1 {
		cmd = exec.Command(parts[0])
	} else {
		cmd = exec.Command(parts[0], parts[1:]...)
	}
	cmd.Dir = rootDir
	// 和 DefaultPTYFactory 走**同一个** ptyEnv：给使用者的 shell 造环境这件事只有一份实现。
	//
	// 这里此前根本没有设 Env，于是整个宿主环境原样漏进 shell —— 包括宿主自己当初从某个 agent CLI
	// session 里被拉起来时沾上的身份标记（见 agentSessionMarkers）。两条 PTY 路径只修一条，等于
	// 没修：使用者走的是哪条取决于会话类型，而 bug 不挑路径。顺带这条路径连 TERM 都没设过。
	//
	// **必须是 cmd.Environ() 而不是 os.Environ()**：Go 只在 `Cmd.Env == nil` 时才按 `Cmd.Dir` 把
	// `PWD` 改写成新目录；一旦我们显式赋了 Env，那层同步就没了，shell 里的 `$PWD` 会停在宿主的
	// 启动目录上 —— 于是提示符、启动脚本、以及一切读 `$PWD` 的工具都会指着一个它并不在的目录。
	// cmd.Environ() 是「Dir 已生效后的那份环境」，把它交给 ptyEnv 才能同时保住这个不变量。
	// （讽刺的是这和我们正在修的 cwd 问题是同一类：目录说谎，而且一声不吭。）
	cmd.Env = ptyEnv(cmd.Environ())
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
	return cmd
}
