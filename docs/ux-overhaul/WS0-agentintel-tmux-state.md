# WS0 — agentintel SSOT 包 + TmuxState 服务

公开可复用 web-terminal 模块 (`github.com/brightman-ai/deepwork-terminal`) 现已自带
tmux 拓扑 / prefix / agent-status 检测能力。本文件记录交付物、公开 API、消息协议
与 standalone↔pro 共存设计。

---

## 1. 交付物清单

### 新建包 `agentintel/` (top-level, 非 internal — pro 可 import)

从 `deepwork-pro/internal/agent_intel/` **逐字移植**的可移植检测核心 (`package agentintel` 不变):

| 文件 | 说明 | 依赖 |
|------|------|------|
| `agent_state.go` | AgentTool/AgentStatus/WaitReason 枚举 + AgentState/AgentIntelResponse | stdlib |
| `process_inspector.go` (+test) | `ps -ewwaxo` 进程树遍历 + claude/codex/gemini/opencode 检测，3s 缓存共享单例 | stdlib |
| `tmux_prober.go` (+test) | `list-panes -s` / `capture-pane`，经 `$TMUX` socket 注入 | stdlib |
| `output_analyzer.go` (+test) | 终端输出结构化分析 → PromptState | stdlib |
| `claude_driver.go` / `codex_driver.go` (+tests) | JSONL 驱动的 token/状态解析 | stdlib |
| `counted_usage.go` / `jsonl_reader.go` / `project_locator.go` (+tests) | JSONL 读取 + 项目目录定位 | stdlib |
| `pty_idle_source.go` | PTY 空闲信号接口 | stdlib |
| `snapshot_watcher.go` | 轮询式 tmux 状态广播器 | stdlib |
| `watcher.go` | fsnotify JSONL tail 状态广播器 | `fsnotify` |
| `observe.go` | kit/obs 指标 + 日志合并器 | `kit/obs` |

### 新增能力 (本任务新写)

| 文件 | 内容 |
|------|------|
| `agentintel/tmux_state.go` | **新** TmuxStateService + TmuxState 聚合 (见 §2) |
| `agentintel/resolver.go` | **新** 泛型 `AgentStateResolver` 类型 (snapshot_watcher 依赖) |

### 编排层移植 + 解耦 (WS0 增量 — 原"未移植"取舍已收口)

`watcher_manager.go` 现已移入 `agentintel/`，但**与 terminal 核心彻底解耦**：移植时
**删除了 `import "github.com/brightman-ai/deepwork-terminal"`**，用包内的
`SessionActivity` 抽象替换原先的 `*terminal.SessionInfo` 依赖。

| 文件 | 说明 |
|------|------|
| `agentintel/watcher_manager.go` | `AgentIntelMonitorManager` + `SessionActivity`/`SessionActivityGetter` 抽象 |
| `agentintel/watcher_manager_test.go` | resolver/session 双模式 + ref-count + not-found 回归测试 |

- 原 `terminalSessionGetter` / `terminalSessionPTYSource` / `toolFromSessionEngine`
  → 改写为 `SessionActivityGetter` / `sessionActivityPTYSource` / `toolFromEngine`，
  **零 terminal/pro 类型耦合**。`SessionActivity` 只暴露 monitor 真正需要的 4 个方法：
  `WorkingDir() / Engine() / LastActivity() / TailLines(n)`。
- `grep -rn 'brightman-ai/deepwork-terminal' agentintel/` → **空** (无自导入环)。
- 结果：`agentintel` 成为**自包含、传输无关、会话模型无关**的 agent-intelligence 引擎，
  pro 可直接 import 本包并**删除自己的 `internal/agent_intel/` 副本** (SSOT 收口由 WS-pro 完成)。

### terminal 侧适配 + 原生默认推送 (TASK B)

| 文件 | 变更 |
|------|------|
| `agent_state_provider.go` (新) | `sessionActivity` 适配 `*Session`→`agentintel.SessionActivity`；`(*SessionManager).sessionActivityGetter()`；`(*Server).newAgentIntelMonitor()`；`nativeAgentStatePush()` 把 monitor 的 typed 流编码为 `AgentStatePushFunc` 的 JSON 流 |
| `server.go` | `NewServer` 在 `hooks.AgentStatePush == nil` 时注入原生默认 push (host 注入者优先) |

### 服务端接线 (terminal 包内)

| 文件 | 变更 |
|------|------|
| `tmux_provider.go` (新) | `TmuxStateProvider` 接口 + 默认 in-process provider + `WithTmuxProvider` Option |
| `tmux_handlers.go` (新) | `GET /tmux/state`、`GET /tmux/prefix` REST handler |
| `server.go` | Server 增 `tmuxProvider` 字段；NewServer 默认注入；注册两条路由 |
| `types.go` | 新增 `MsgTypeTmuxState = "tmux_state"` 常量 |
| `handlers.go` | WS writer goroutine 增 ~1s ticker：轮询 TmuxState，diff 后仅变化时推送 |
| `go.mod` / `go.sum` | 新增直接依赖 `github.com/fsnotify/fsnotify v1.10.0` |

---

## 2. 新增公开 API 面

### agentintel 包

```go
// 聚合快照
type TmuxState struct {
    Installed     bool
    ServerRunning bool
    Attached      bool
    Prefix        TmuxPrefix
    Sessions      []TmuxSessionState
}
type TmuxPrefix       struct { Display string; Bytes []byte }      // C-b → 0x02, C-a → 0x01
type TmuxSessionState struct { Name string; Attached bool; Windows []TmuxWindowState }
type TmuxWindowState  struct { Index int; Name string; Active bool; Panes []TmuxPaneState }
type TmuxPaneState    struct { Index int; Active bool; Title, CWD string; PID int;
                               AgentTool AgentTool; AgentStatus AgentStatus }

// 服务 (内部缓存 prefix+installed；单次批量 tmux 查询拼拓扑；共享 ps 快照做 per-pane 检测)
func NewTmuxStateService() *TmuxStateService
func (s *TmuxStateService) TmuxInstalled() bool                       // which tmux，60s 缓存
func (s *TmuxStateService) Prefix(ctx) TmuxPrefix                     // show-options -g prefix，10s 缓存，缺省 C-b
func (s *TmuxStateService) ServerRunning(ctx) bool                    // list-sessions 是否成功
func (s *TmuxStateService) Attached(ctx, shellPID int) bool           // 该 shell 进程树里有无 tmux client
func (s *TmuxStateService) State(ctx, shellPID int) TmuxState         // 全量快照

// 泛型 resolver 种子
type AgentStateResolver func(ctx, sessionID string) (AgentIntelResponse, error)

// 解耦的会话视图 + 编排层 (host-agnostic — 任何会话模型实现 SessionActivity 即可接入)
type SessionActivity interface {
    WorkingDir() string          // 定位 agent JSONL transcript
    Engine() string              // 选择 JSONL driver ("claude"/"codex"/…)
    LastActivity() time.Time     // PTY 空闲检测
    TailLines(n int) []string    // 终端输出尾行 → 输出分析
}
type SessionActivityGetter func(ctx, sessionID string) (SessionActivity, bool)

// 每 session 的 agent-state 广播器 (ref-count 共享 watcher)
func NewAgentIntelMonitorManager(getter SessionActivityGetter, locator *ProjectLocator) *AgentIntelMonitorManager
func NewAgentIntelMonitorManagerWithResolver(getter SessionActivityGetter, locator *ProjectLocator, resolver AgentStateResolver) *AgentIntelMonitorManager
func (m *AgentIntelMonitorManager) Subscribe(ctx, sessionID string) (<-chan AgentIntelResponse, func(), error)
```

**双模式**: `resolver != nil` → resolver 模式 (SnapshotWatcher 轮询 resolver，`getter` 可为
`nil`)；否则 → session 模式 (JSONL `AgentStateWatcher` tail transcript + PTY 空闲源)。

**pro 迁移签名变化 (唯一)**: 第一个入参类型由 `*terminal.SessionInfo` 派生的
`terminalSessionGetter` 改为 `agentintel.SessionActivityGetter`。pro 现有调用
`NewAgentIntelMonitorManagerWithResolver(nil, locator, resolver)` **传 `nil`、走 resolver 模式**，
迁移后**签名完全兼容**——把 import 从 `deepwork/internal/agent_intel` 改成
`github.com/brightman-ai/deepwork-terminal/agentintel` 即可，调用点无需改。

性能: 所有 tmux/ps 子进程调用都套 1.5s context 超时；无 server 时退化为空 session 列表而非报错。

### terminal 包

```go
type TmuxStateProvider interface {
    TmuxState(ctx context.Context, shellPID int) (json.RawMessage, error)
}
func WithTmuxProvider(p TmuxStateProvider) Option   // host 可注入更丰富实现；standalone 无需
```

---

## 3. tmux_state 消息协议

REST:
- `GET /api/tmux/state`  → `TmuxState` JSON (见下)。可选 `?session=<id>` 把 `attached` 作用域到该 session 的 shell。
- `GET /api/tmux/prefix` → 仅 `{ display, bytes }`。
- 二者均经 `authWrap`，与现有路由同模式 (`X-Auth-Code` / `X-CLI-Auth` / `?auth=` / cookie)。
- 嵌入模式下前缀为 host 挂载点；standalone 下为 `/api`。

WebSocket 推送 (文本帧，复用现有 `WSControlMessage` 信封):
```json
{ "type": "tmux_state", "payload": { ...TmuxState... } }
```
- WS writer goroutine 内 ~1s ticker 重算 → 与上次字节 diff → **仅变化时推送** (省带宽)。
- provider 调用内部已超时，不会长时间阻塞写循环。

`payload` (TmuxState) 实测样例 (prefix=C-b 时 bytes 为 base64 `"Ag=="`=0x02):
```json
{
  "installed": true, "serverRunning": true, "attached": false,
  "prefix": { "display": "C-b", "bytes": "Ag==" },
  "sessions": [{
    "name": "devwork", "attached": false,
    "windows": [{ "index": 2, "name": "server", "active": true,
      "panes": [{ "index": 1, "active": true, "pid": 733649,
                  "cwd": "/...", "agentTool": "claude", "agentStatus": "running" }]
    }]
  }]
}
```
`agentStatus` 由 `capture-pane` (40 行) → `AnalyzeOutput` 推导:
NeedsPermission→`waiting`，Idle/LikelyIdle→`idle`，Running/默认→`running`。

---

## 4. standalone 与 pro-hook 共存

- **standalone**: `NewServer` 在 `tmuxProvider == nil` 时注入 `newDefaultTmuxProvider()`
  (基于 `agentintel.TmuxStateService`)。无需 pro、无需任何 hook，开箱即有 tmux 拓扑/prefix/agent-status。
- **agent_state 原生推送 (TASK B)**: `NewServer` 在 `Hooks.AgentStatePush == nil` 时注入
  `nativeAgentStatePush(s.newAgentIntelMonitor())`——基于本包 `AgentIntelMonitorManager`
  over terminal 自己的 live sessions (`sessionActivity` 适配 `*Session`)，使 **standalone 开箱
  即有 per-session `agent_state` WS 推送**，无需 pro、无需 hook。
- **pro / host**: 可用 `WithTmuxProvider(...)` 注入更丰富 provider，**覆盖**默认值。这是**纯附加**:
  - `Hooks.AgentStatePush` 若被 host 注入则**优先于原生默认** (back-compat)；不注入则用原生默认。
  - `tmux_state` 是 terminal 自有的、附加的新消息类型，与 `agent_state` 正交。
  - `Server.Handler()` / embed / mount-prefix 行为不变 (只多两条相对路由)。

---

## 5. 验证证据 (Creator 级 编译 + 运行时)

- `GOPROXY=https://goproxy.cn,direct go build ./...` → **PASS**
- `go vet ./agentintel/...` → **PASS**；`go test ./agentintel/...` → **ok** (移植的全部单测)
- `go test .` (server 包) → **ok**
- `go mod tidy` 后 fsnotify 为直接依赖，build 仍 PASS
- **运行时**: 构建二进制 → 在 tmux 会话 (devwork: editor + server 两窗口共 3 pane) 内
  以 `:8091 -auth-code testauth123` 启动 → `GET /api/tmux/state` 返回 200 + 完整拓扑:
  - `installed/serverRunning=true`，列出全部 3 个 pane (含 PID/CWD/active)
  - prefix 默认 `C-b`→`Ag==`(0x02)；`set -g prefix C-a` 后重查得 `C-a`→`AQ==`(0x01)，证明**动态解析非硬编码**
  - agent 检测命中: 某 pane `agentTool=claude / agentStatus=running`
  - 验证后已停服 + kill tmux + 清理临时文件，端口 8091 干净
- pro `internal/agent_intel/` git status 未变 (未删未改)
