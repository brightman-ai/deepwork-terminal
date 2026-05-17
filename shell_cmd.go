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
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
	return cmd
}
