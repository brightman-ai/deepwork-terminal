# Deepwork-Terminal 体验改造 — 主计划 (SSOT)

> 本文件是本轮改造的唯一事实源。决策、工作流、验证方式都以此为准。
> 业务输出中文; 代码/ID 英文。终局质量: 全·深·实·冻。

## 0. 背景与约束

- 本工程 = 可远程 web 访问的终端门户 (Go 后端 + Vue3/Vite 前端, xterm.js)。
- 两种用法: ① standalone 直接编译 (public); ② 被 private repo `deepwork-pro` 嵌入成多 app 应用。
- 铁律: **不改坏主体**; 非必要不改 `deepwork-pro` (改前确认)。
- 测试: 用 `deepwork-browser` (dw-browser CLI) 鼠标/键盘/触摸实测; 通过后以 **8087** 端口拉起供 human 实测。
- 角色纪律: Creator ≠ Verifier — 实现 agent 只做编译级自证; live 验收由 Coordinator/独立 verifier 在 8087 上跑。
- UX 范式参考: `deepwork-pro/docs/changes/CHG-013-ux-v6-landing/refs/deepwork-v6.html` + dw-pro design tokens (`deepwork/frontend/src/css/tokens.css`, `--dw-*`)。

## 1. 决策记录 (冻结)

| ID | 决策 | 备注 |
|----|------|------|
| D1 | tmux 切 pane = **顶部 header pane 标签行**(检测到 session 才显示, 点击切) + **底部常驻 tmux 快捷区**(检测到装 tmux 即显示) | 终端标题行保留; attach 按钮未 attach 排最前、已 attach 排后 |
| D2 | 通知 = **完整 PWA + Web Push** (VAPID), 前台 Notification 兜底 | iOS 16.4+ 支持但无自动 prompt → 必须图文引导 |
| D3 | tmux/agent 检测 = **SSOT 下沉到 terminal public 的 `agentintel` 包** | pro 改为 import 它、删副本 (WS-pro, 需 module 版本协调) |
| D4 | PWA 安装引导入口 = 顶栏小图标(主, 平台感知) + tmux header 小铃铛(次, 语境化) | 同一引导 sheet; 已 standalone 则隐藏/转通知开关 |
| D5 | tmux prefix **动态读取** (后端 `tmux show-options -g prefix`), 全前端去硬编码 `\x02` | C-b→0x02, C-a→0x01 |

## 2. 架构: agentintel SSOT (已落地 WS0)

- 新公共包 `github.com/brightman-ai/deepwork-terminal/agentintel` (非 internal, 供 pro import)。
- 检测核心从 pro 移植 (tmux_prober/process_inspector/agent_state/claude_driver…, 仅依赖 stdlib + kit/obs)。
- 新增 `TmuxStateService`: `TmuxInstalled` / `Prefix` / `ServerRunning` / `Attached` / `State`。
- 聚合 `TmuxState{Installed, ServerRunning, Attached, Prefix{display,bytes}, Sessions[]{…Windows[]{…Panes[]{Index,Active,Title,AgentTool,AgentStatus}}}}`。
- Server: REST `GET /tmux/state`, `GET /tmux/prefix`; WS 控制帧 `tmux_state` (~1s diff push)。
- standalone 默认内置 provider (零 host 配置); host 可 `WithTmuxProvider` 覆盖; 既有 Hooks/Handler/mount-prefix 不变。
- 契约细节: `docs/ux-overhaul/WS0-agentintel-tmux-state.md`。

## 3. 工作流 (WS) 与依赖

| WS | 内容 | 依赖 | 状态 |
|----|------|------|------|
| WS0 | agentintel SSOT 包 + TmuxState 服务 (后端) | — | ✅ done (runtime 实证) |
| WS1 | copy 模式滚动期选区错乱修复 | — | ✅ 编译级, 待 8087 验收 |
| WS2 | 动态 tmux prefix (去硬编码) | WS0 | 进行中 |
| WS3 | 顶部 header tmux pane 标签行 (点击切 + 状态点) | WS0 | 进行中 |
| WS4 | 底部常驻 tmux 快捷区 + attach 动态排序 + split/zoom 状态机 | WS0 | 待 |
| WS8 | tmux 状态 sheet (点 tmux: 标签区) | WS0 | 待 |
| WS6 | 剪贴板粘贴 (Ctrl粘滞+v / Paste) 失效修复 | — | 待 |
| WS5 | 右侧收纳抽屉 (图片/文件/输入分类预览, dw-aiside 风格) | — | 待 |
| WS7 | PWA + Web Push 通知 + 平台感知安装引导 | WS0 | 待 |
| WS-pro | pro repoint 到 terminal/agentintel + 删副本 | WS0 | 待 (改 pro, 需确认+module 协调) |

## 4. 关键设计要点

- **WS3 header pane 行**: 在 xterm 顶部、终端标题行下。`tmux: 1 2 3 4 +`。点数字→ `prefix + select-window`/`select-pane`。per-pane 状态点: agent running=中性点, waiting=红点 (来自 TmuxState.AgentStatus)。仅 `Attached/ServerRunning` 时渲染。
- **WS4 底部 tmux 区**: `TmuxInstalled` 即显示。键: cp(copy-mode)、PgUp、PgDn、↑、↓、SpC(space)、^C(红)、vsplit、hsplit、zoom、sessions、attach、detach。全部走动态 prefix。split/zoom 状态机: 未分屏→分屏; 已分屏→向下循环切; zoom 1 图标 toggle。attach 动态排序: 未 attach→最前; 已 attach→末尾。点 `tmux:` 标签区→ WS8 状态 sheet。
- **WS5 抽屉**: 数据源 = clipboard 上传 (`{cwd}/tmp/clip`, `tmp/files`) + ComposeBar history (server store `~/.dw-terminal/store.json`)。需后端: 列资源 endpoint + 缩略图/静态服务。mobile=右侧滑出抽屉(330px/calc(100vw-24px)); PC=可收缩侧栏。dw-aiside (`--dw-*`) 风格。
- **WS6 粘贴**: 根因 = Ctrl粘滞+v 发 `\x16` 给 PTY 无粘贴语义; 真粘贴走 `navigator.clipboard.readText`, HTTP/iOS 下 `clipboard` undefined 或被 `.catch` 静默吞。修: 明确粘贴路径 + iOS HTTP fallback + 失败可见提示。
- **WS7 通知**: manifest + service worker (Vite PWA) + VAPID (后端生成/存) + PushManager.subscribe; 后端存 subscription, agent→waiting 时发 push; 前台 Notification 兜底。安装引导见 D4。

## 5. 风险

- R1: pro 以 pinned module 依赖 terminal (非 local replace) → WS-pro 收口需临时 `replace => ../deepwork-terminal` 或 tag+bump。迁移期 terminal/pro 短暂双份 (正常迁移姿态)。
- R2: 共享文件 (Toolbar.vue / TerminalPage.vue / TmuxPanel.vue) 多 WS 触碰 → **串行**派工避免冲突。
- R3: iOS Safari Web Push 仅对「添加到主屏」的 PWA 生效, 无自动 prompt → 引导必做。
- R4: dw-browser 触摸/滚动模拟与真机 Safari 惯性滚动有差异 → WS1 仍需 human 真机复测。
