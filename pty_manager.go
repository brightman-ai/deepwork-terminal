// Package terminal — pty_manager.go: PTY spawn/stop/watch goroutines [T5 §4]
package terminal

import (
	"context"
	"os"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"github.com/creack/pty"

	"github.com/brightman-ai/kit/log"
)

var ptyLogger = log.Module("terminal.pty")

// spawnPTY 启动 PTY 进程，绑定 rootDir 作为 cwd。
// shellCmd 为可配置的 shell 命令字符串（如 "/bin/bash --login"），空字符串使用默认值。
// 返回 PTY master fd 和 exec.Cmd。[T5 §4 step 2]
func spawnPTY(ctx context.Context, rootDir string, shellCmd string) (*os.File, *spawnedCmd, error) {
	_ = ctx // reserved for future timeout propagation
	cmd := newShellCmd(rootDir, shellCmd)
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 220, Rows: 50})
	if err != nil {
		return nil, nil, err
	}
	return ptmx, &spawnedCmd{cmd: cmd}, nil
}

// stopPTY 优雅关闭 PTY：先发 exit，等待 500ms，再 kill。[T5 §4 Stop]
func stopPTY(ptmx *os.File, sc *spawnedCmd) {
	if ptmx == nil {
		return
	}
	// 尝试优雅退出
	_, _ = ptmx.Write([]byte("exit\n"))

	done := make(chan struct{})
	go func() {
		defer close(done)
		if sc != nil {
			_ = sc.cmd.Wait()
		}
	}()

	select {
	case <-done:
		// 正常退出
	case <-time.After(500 * time.Millisecond):
		// 超时，强制 kill
		if sc != nil && sc.cmd.Process != nil {
			pgid, err := syscall.Getpgid(sc.cmd.Process.Pid)
			if err == nil && pgid == sc.cmd.Process.Pid {
				_ = syscall.Kill(-pgid, syscall.SIGKILL)
			} else {
				_ = sc.cmd.Process.Kill()
			}
		}
	}
	_ = ptmx.Close()
}

// startReadLoop 启动 PTY stdout 读取 goroutine：
// 持续读取 PTY 输出 → Ring.Write + WSConn.WriteMessage（若已连接）。[T5 §4 step 4]
// 退出时调用 onExit(exitCode)。
func startReadLoop(cs *SessionV2, onExit func(exitCode int)) {
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := cs.Pty.Read(buf)
			if n > 0 {
				data := make([]byte, n)
				copy(data, buf[:n])
				cs.Ring.Write(data)

				// 向 WS 推送 output 消息
				cs.mu.Lock()
				conn := cs.WSConn
				cs.mu.Unlock()
				if conn != nil {
					msg := buildOutputMsg(data)
					wctx, wcancel := context.WithTimeout(context.Background(), 5*time.Second)
					_ = conn.Write(wctx, websocket.MessageText, msg)
					wcancel()
				}
			}
			if err != nil {
				ptyLogger.Debug("pty read ended", "sessionID", cs.SessionID, "err", err)
				exitCode := 0
				if cs.Cmd != nil && cs.Cmd.ProcessState != nil {
					exitCode = cs.Cmd.ProcessState.ExitCode()
				} else if cs.Cmd != nil {
					_ = cs.Cmd.Wait()
					if cs.Cmd.ProcessState != nil {
						exitCode = cs.Cmd.ProcessState.ExitCode()
					}
				}
				onExit(exitCode)
				return
			}
		}
	}()
}

// resizePTY 更新 PTY 行列数 [T5 §5.2 resize]
func resizePTY(ptmx *os.File, cols, rows uint16) error {
	if ptmx == nil {
		return nil
	}
	return pty.Setsize(ptmx, &pty.Winsize{Cols: cols, Rows: rows})
}
