// Package terminal — protocol.go: T5 §5 WebSocket 终端消息协议定义 (r2)
// BS-08 v2 消息类型：input/output/resize/ping/pong/closed
package terminal

import "encoding/json"

// ─────────────────────────────────────────────
// T5 §5.2 客户端→服务端消息类型常量
// ─────────────────────────────────────────────

const (
	// MsgInput 键盘输入 / 控制序列 (客户端→服务端)
	MsgInput = "input"
	// MsgResize 窗口大小变化 (客户端→服务端)
	MsgResize = "resize"
	// MsgPing 心跳 (客户端→服务端)
	MsgPing = "ping"
)

// ─────────────────────────────────────────────
// T5 §5.3 服务端→客户端消息类型常量
// ─────────────────────────────────────────────

const (
	// MsgOutput PTY 输出 ANSI 透传 (服务端→客户端)
	MsgOutput = "output"
	// MsgClosed 终端关闭通知 (服务端→客户端)
	MsgClosed = "closed"
	// MsgPong 心跳响应 (服务端→客户端)
	MsgPong = "pong"
)

// ─────────────────────────────────────────────
// ClosedReason: closed 消息 reason 枚举
// ─────────────────────────────────────────────

const (
	ClosedReasonNormal     = "normal"
	ClosedReasonCrashed    = "crashed"
	ClosedReasonStopCalled = "stop_called"
)

// ─────────────────────────────────────────────
// 消息结构体
// ─────────────────────────────────────────────

// TermMsg 是 WebSocket 文本帧的统一 JSON 格式 [T5 §5.2 §5.3]
type TermMsg struct {
	Type     string `json:"type"`
	Data     string `json:"data,omitempty"`     // input / output
	Cols     int    `json:"cols,omitempty"`     // resize
	Rows     int    `json:"rows,omitempty"`     // resize
	Reason   string `json:"reason,omitempty"`   // closed
	ExitCode int    `json:"exit_code,omitempty"` // closed
}

// buildOutputMsg 构建 output 消息的 JSON 字节
func buildOutputMsg(data []byte) []byte {
	msg := TermMsg{Type: MsgOutput, Data: string(data)}
	b, _ := json.Marshal(msg)
	return b
}

// buildClosedMsg 构建 closed 消息的 JSON 字节
func buildClosedMsg(reason string, exitCode int) []byte {
	msg := TermMsg{Type: MsgClosed, Reason: reason, ExitCode: exitCode}
	b, _ := json.Marshal(msg)
	return b
}

// buildPongMsg 构建 pong 消息的 JSON 字节
func buildPongMsg() []byte {
	msg := TermMsg{Type: MsgPong}
	b, _ := json.Marshal(msg)
	return b
}
