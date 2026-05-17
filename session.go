// Package terminal — session.go: SessionV2 内存对象 [T5 §2.1]
// BS-08 v2: Int64 SessionID, WorkspaceID, PTY, Ring, WS 连接
package terminal

import (
	"context"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// SessionV2 是 BS-08 v2 维护的运行时状态（内存，不持久化）。
// Session 持久化由 BS-03 的 conversationDB 负责。[T5 §2.1]
type SessionV2 struct {
	SessionID   int64            // 关联 BS-03 Session.id (Int64)
	WorkspaceID int64            // 关联 BS-10 Workspace.id (Int64)
	RootDir     string           // Workspace.root_dir (PTY cwd)
	Pty         *os.File         // PTY master fd
	Cmd         *exec.Cmd        // Shell 进程
	Ring        *RingBuffer      // 环形缓冲 (一屏内容 ≈ 4KB)
	WSConn      *websocket.Conn  // 当前 WebSocket 连接 (可为 nil)
	State       string           // spawning | running | stopping | stopped | failed
	mu          sync.Mutex
}

// Lock 加锁保护 WSConn / State 访问
func (cs *SessionV2) Lock() { cs.mu.Lock() }

// Unlock 释放锁
func (cs *SessionV2) Unlock() { cs.mu.Unlock() }

// GetState 返回当前状态（线程安全）
func (cs *SessionV2) GetState() string {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.State
}

// SetState 设置状态（线程安全）
func (cs *SessionV2) SetState(s string) {
	cs.mu.Lock()
	cs.State = s
	cs.mu.Unlock()
}

// SetWSConn 替换 WebSocket 连接（关闭旧连接），[T5 §6.1]
// 返回是否关闭了旧连接
func (cs *SessionV2) SetWSConn(conn *websocket.Conn) bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	hadOld := cs.WSConn != nil
	if cs.WSConn != nil {
		_ = cs.WSConn.CloseNow()
	}
	cs.WSConn = conn
	return hadOld
}

// WriteWS 向当前 WebSocket 发送消息（线程安全）
// 若无连接则静默忽略
func (cs *SessionV2) WriteWS(data []byte) {
	cs.mu.Lock()
	conn := cs.WSConn
	cs.mu.Unlock()
	if conn != nil {
		wctx, wcancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = conn.Write(wctx, websocket.MessageText, data)
		wcancel()
	}
}

const (
	// AdapterStateSpawning PTY 正在启动
	AdapterStateSpawning = "spawning"
	// AdapterStateRunning PTY 运行中
	AdapterStateRunning = "running"
	// AdapterStateStopping PTY 正在停止
	AdapterStateStopping = "stopping"
	// AdapterStateStopped PTY 已停止（正常）
	AdapterStateStopped = "stopped"
	// AdapterStateFailed PTY 异常退出
	AdapterStateFailed = "failed"
)
