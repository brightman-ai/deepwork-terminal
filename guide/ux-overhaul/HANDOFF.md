# Handoff — deepwork-terminal 两仓/三仓 SSOT 后续开发

> 面向接续本工作的新 agent / 开发者。详细设计与验收证据见 `deepwork-terminal/docs/ux-overhaul/`（`PLAN.md` 主计划 + `WS0-agentintel-tmux-state.md` / `WS5-resource-drawer.md` / `WS7-pwa-push.md` + `accept/*.png` 截图）。本文只补“架构心智 + 约定 + 现状 + 待办”，不复述已在文档/commit 里的内容。
> 记忆库已沉淀：`agentintel-ssot` · `ux-overhaul-2026-06` · `pro-terminal-embedding`（recall 时会带出）。

## 1. 三仓架构与分层 SSOT（终局，已落地）

三个本地兄弟仓，分层依赖（低→高）：
- `~/code/stwork/deepwork`（**@ce** 核心框架：MainLayout/portal 导航/布局/axios/assistant-stream/`useEdgeDrag`/`MobilePortalTrigger`）
- `~/code/stwork/deepwork-terminal`（**终端**：xterm 内核 + 全部 per-surface chrome + `agentintel` 检测/编排引擎 + Go server）
- `~/code/stwork/deepwork-pro`（**宿主**：多 app v2 portal 框架，嵌入终端 + 其它 portal）

**链接方式（本地 sibling workspace —— 这是终局正解，勿改成发布依赖）：**
- Go 后端：`deepwork-pro/go.mod` 用 `replace github.com/brightman-ai/deepwork-terminal => ../deepwork-terminal`。
- 前端：vite 别名 `@terminal` → `../../deepwork-terminal/frontend/src`，`@ce` → `../../deepwork/frontend/src`（pro 与 terminal 都用）。
- **为什么保留 replace 不转 require**：前端是本地 alias（非发布的 npm 包），Go 若转版本化 require 会与前端不一致 + 冻结快照 + 杀掉本地联调。co-develop 场景下 replace+alias 才一致、才是 SSOT。详见本会话末尾的判断（option A 已被采纳）。

**SSOT 铁律（违反就会出本会话踩过的漂移 bug）：**
- 终端 UI 唯一源 = `components/terminal-session/CliTerminalSurface.vue`。**所有 per-surface chrome**（状态行/TmuxPaneBar/TmuxQuickBar/Toolbar/ResourceDrawer/InstallNotifyIcon/KeyCastr/overlays）住在 surface 里，**不要**再分散到各 host（CliPortal / pro CliV2）——否则“A 页加了 B 页没加”必然漂移（本会话因此修了多次）。host 只负责 tab 栏 + 会话生命周期 + 多 app 框架。
- 共享 affordance 放**最低层 @ce**：`useEdgeDrag`（边缘可拖）、`MobilePortalTrigger`（移动门户导航钮）都在 @ce，terminal 与 pro 共用同一份；不在高层复制。
- `agentintel`（tmux/进程/claude-codex 检测 + waiting 状态 + 编排）是 terminal 的**顶层包**（`deepwork-terminal/agentintel/`，非 internal，pro 可 import）；pro 不再有副本。检测核心自包含、零 terminal-core 耦合（用 `SessionActivity` 接口解耦）。

## 2. 构建 / 运行 / 测试工作流

- **terminal**：`cd deepwork-terminal && ./build.sh`（vue build → 嵌入 `internal/spa/dist` → 产出 `./dw-terminal`）。跑：`env -u TMUX HOME=/home/ubuntu ./dw-terminal -addr 0.0.0.0:8087 -auth-code <code> -shell "/usr/bin/zsh -l"`。
- **pro**：`cd deepwork-pro/frontend && npx vite build`（嵌入 `internal/webui/spa/dist`）+ `GOEXPERIMENT=jsonv2 GOPROXY=https://goproxy.cn,direct go build -o bin/deepwork ./cmd/deepwork`；跑 `./bin/deepwork --webui --port 8088`（`RunWebUI` 绑 0.0.0.0；`--data` 指定数据目录）。或 `make run`。
- **后台启动坑**：本沙箱里 `nohup &` / 内联 `sleep` 常 exit 144；用 harness 的 run_in_background 或 `setsid env -u TMUX HOME=… <bin> … >log 2>&1 </dev/null &`。务必 `env -u TMUX`（否则嵌套 tmux 把进程顶掉）+ 显式 `HOME=/home/ubuntu`（否则 `os.UserHomeDir` 空 → 数据落 cwd / `/inputs` 报错）。
- **实测**：用 `~/code/stwork/deepwork-browser`（`dw-browser` CLI：open/screenshot/act/eval，`--device iphone-14`）。headless 限制：剪贴板/通知需 CDP 强制聚焦+授权；`display-mode:standalone` 不被 headless honor（safe-area 类只能靠 computed-style 断言 + 真机复测）。
- **PWA / 通知 / 一键粘贴必须 HTTPS**（Service Worker/Push/clipboard.readText 仅安全上下文；localhost 例外，HTTP 的 LAN/Tailscale IP **不行**）。取 HTTPS：内置 Cloudflare 隧道（Settings→Network→Internet Access，或 `/tunnel/start`）/ `tailscale serve --bg --https=443 http://127.0.0.1:PORT`。注意重启 server 会杀 cloudflared 子进程、trycloudflare 域名会变（换域名要重新订阅，push 订阅按 origin）。
- **Creator≠Verifier**：实现 agent 只做编译级自证，live 验收（dw-browser + 8087/8088）由 Coordinator/独立 verifier 做。

## 3. 关键约定与坑（本会话血泪沉淀）

- **safe-area inset 只在 shell-root 一处**：`@ce main.css` 的 `[data-testid=main-layout].dw-app-viewport-frame` 唯一拥有 `padding-top: env(safe-area-inset-top)`；**任何 host/子组件再加一遍 = 双重预留**（pro 的 `.dw-m-ctx` 曾因此屏占比掉 11%）。底部 inset 用 `@media (display-mode: standalone)` 门控（Safari 标签页的 visualViewport 已排除底栏，再加会双重留白 34px）。
- **PWA 底部 home-indicator 区发灰** = 浅色 shell body 透出；terminal 已用 `index.html` 内联 `html,body{background:#0d0d0f}` 补；根因是 **terminal 标准 shell 没加 `.dark` class**（整体浅色 shell + 深色终端），**明暗统一是待办**（见 §5）。pro 原生 `<html class=dark>`，无此问题。
- **VAPID `sub` 传裸邮箱**：webpush-go v1.4.0 `vapid.go` 会对非 `https:` 的 subscriber **自动前缀 `mailto:`**；若你传 `mailto:x@y` → 变成 `mailto:mailto:x@y` 非法 → Apple 403 `BadJwtToken`（且旧代码只在 404/410 prune、静默吞 403）。`config.go` 现存裸邮箱 + `resolveVapidSubscriber` 剥前缀防御；`/push/test` 现返回真实投递结果（送达/被拒+状态码）。Apple 403 三因：aud≠push 源 / exp>24h / sub 非 URL|mailto。
- **pro 转发终端 API 用 gin `NoRoute` 兜底**（`internal/webui/terminal_routes.go`），不要用显式路由白名单——否则 terminal 每加一个 `/api/cli/*` 路由（tmux/uploads/inputs/push）pro 都会 404 stale。pro 自有 param 路由（agent-state/stream）优先，其余落到终端 handler。
- **`-shell` 支持带参**：`session_manager.go` 已 shell-words 分词（`"tmux attach -t x"` / `"zsh --login"` 可用）。
- **copy 模式落点**对准悬浮球“箭头尖”（`touchballCursorGeometry.ts` 单一几何源）；选区期 `touch-action:none` + 原子 viewportY 快照防滚动错乱。
- **跨仓改 pro 时**：若 pro 主检出有并发 session WIP，用 isolated git worktree（sibling 路径让相对 alias 解析 + symlink 复用 node_modules）改完再 merge 回，别扰乱活动 checkout。

## 4. 当前状态（已交付）

- 三仓均已合 main 且本地构建绿：**terminal `main`@`51fa733`**（58 commit 全 FF，feature 分支已删）、**@ce `main`**（含 MobilePortalTrigger 9a2ff5e 等）、**pro `main`@`d89c9cf`**（sidenav SSOT）。
- `8087`（terminal standalone）+ `8088`（pro 统一）均在 `0.0.0.0` 运行最新二进制；auth `accept123`；pro `--data /tmp/dw-accept-data`。Tailscale `100.125.7.50` / LAN `192.168.19.143`。
- **未推送**：所有 commit 仅本地，`origin/main` 未更新（terminal 有 github remote，pro/@ce 视情况）。
- 已交付的用户级能力：copy 模式、tmux header pane 行 + 底部常驻 tmux 区（动态 prefix）、跨 session 收纳抽屉（索引 + transcript 输入）、剪贴板粘贴修复、PWA+Web Push（已真机验证生效）、安装/通知平台感知引导、可拖边缘把手、设置入导航 + 移动端粘性分类导航。

## 5. 待办 / 下一步（按优先级）

1. **明暗统一**（体验债）：terminal 标准 shell 是浅色（`@ce :root` 浅、`.dark` 未应用），仅 body 打了深色补丁。终局应让 standalone terminal 整体深色（应用 `.dark` 或调 `:root`），与 pro 一致——注意是 @ce 层全局改动，需评估对 @ce 其它消费者影响。
2. **推送 / 发布**（按需）：`terminal origin/main` 未推送；若要让 pro 把 terminal 当**版本化外部依赖**（而非本地 replace），需 (a) push terminal + tag Go module，(b) 把前端 `@terminal` 也做成发布 npm 包，(c) pro 同时切 Go require + 前端包版本。**仅当要脱离本地联调时才做**；否则保持 replace（已采纳）。
3. **@ce 协调**：@ce 是共享仓，改它（如明暗统一、共享组件）要顾及 assistant-stream 等其它在研工作；本会话约定“只 stage 自己的文件，不碰别人的 WIP”。
4. 真机回归：真剪贴板惯性滚动手感、真 Web Push 在新 cloudflare 域名下的端到端（域名变了要重订阅）。

## 6. 下一会话建议技能

- `/i-resume`（崩溃/暂停恢复，重建上下文）作为开场对齐。
- 改设计/方案前用 `/i-think`（碰撞）；跨子系统改动走对应 Step Card（`/i-design-subsystem` 等）。
- 跨仓提交用 `/git-commit`（语义化分批，记得 commit trailer `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`）。
- 实测/验收用 `~/code/stwork/deepwork-browser`（dw-browser）；验收门：dw-browser explore 通过才算过。
- 经验沉淀用 `/i-td`。
