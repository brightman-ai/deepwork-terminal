# WS5 — 右侧收纳抽屉 (Resource Drawer · 跨会话版)

> **数据层从 session-scoped 改为 CROSS-SESSION**：用户常开新会话复用旧图片/文件/提示词。
> 数据源 = (1) 持久化上传索引 `~/.dw-terminal/uploads.json` (图片 + 文件，跨所有会话)
> + (2) claude/codex **transcript** 解析出的人类输入 (`~/.claude/projects/**`, `~/.codex/sessions/**`)。
> 范式: 右侧 sheet / cards / muted labels / mono accents，复用 TmuxStatusSheet 覆盖层视觉惯例，
> 暗色主题，不引入 oklch tokens。新增 **filter bar** (会话/来源 · 排序 · 搜索)。

## 1. 后端 (Go)

全局 (非 session-scoped) 路由，auth 包裹，mount-prefix 与既有路由一致 (standalone `/api/*`,
嵌入 `/api/cli/*`)：

| 路由 | 行为 |
|------|------|
| `GET /uploads` | 跨会话列出所有图片+文件 (索引回填 alive 会话的 `tmp/clip`/`tmp/files`)，每项 `{id,kind,name,size,mtimeMs,sessionId,sessionName,cwd,url}`，**按 uploadedAtMs 倒序**。`?kind=image\|file` / `?session=<name>` 可过滤。`url` = `…/uploads/raw?id=<id>`。|
| `GET /uploads/raw?id=<id>` | 以 `mime.TypeByExtension` 经 `http.ServeContent` 返回字节；`Cache-Control: private,max-age=86400` + `X-Content-Type-Options: nosniff`。|
| `GET /inputs` | 解析 claude+codex transcript，**仅** human 提示词，`{items:[{text,tsMs,source,cwd,project,sessionName?}]}`，最新在前，去重，上限 200。|

### 上传索引 — `uploads_index.go` (已存在，本次复用)

`uploadEntry{id,kind,absPath,name,size,mtimeMs,sessionId,sessionName,cwd,uploadedAtMs}`；
`uploadIndex` 内存 map + 互斥锁 + JSON 落盘 (沿用 workbench/push store 惯例)，按 `absPath` 去重。
`clipboard_paste.go` 在上传/去重路径 `recordUpload(sess,…)` 写入索引 (带 session name + cwd)。

### 安全 — 结构性免穿越

客户端只传 **不透明 id** (`sha1(absPath)[:12]`)，服务端在索引白名单里查 id → 取记录的 `absPath`
回字节。**没有任何客户端路径到达文件系统**，目录穿越在结构上不可能 (旧版的 `resolveUploadPath`
白名单防御已被 id 白名单取代，更强且更简单)。id 不在索引 / 文件已消失 → 404，无目录列举。
`list()` 每次 re-stat 并剔除已删除文件 (持久化 prune)。

### 输入解析 — `inputs.go` (本次新增)

复用 `agentintel` (`ProjectLocator` + `JSONLReader`)，鲁棒解析、永不 panic：

- **Claude** (`~/.claude/projects/**/*.jsonl`，`ClaudeAllSessionFiles()`)：取 `type:"user"` 行。
  排除 AI/工具/合成：`isMeta`/`isSidechain` 行、`userType != "external"`、含 `tool_result`
  block 的行、`<command-name>`/`<local-command-stdout>` 等斜杠命令回显。`content` 为字符串或
  text block 数组 → 拼接为人类文本。`cwd`/`sessionId`/`timestamp` 取自行顶层。
- **Codex** (`~/.codex/sessions/**/*.jsonl`，`CodexSessionFiles()` 递归 walk)：取
  `event_msg` 且 `payload.type=="user_message"` 的 `payload.message` (干净的人类输入；
  `response_item` 副本被注入的 `# AGENTS.md` 上下文污染，故弃用)。`cwd` 取自 `session_meta`。

合并后按 `tsMs` 倒序、去重 (完全相同文本只留最新一条)、截断 200。空 transcript / 畸形行 /
不可读文件均不报错 — 只是少几条或返回空数组。

`go build ./...` 0 · `go test .` PASS (TC-UP-01..04 + TC-IN-01) · `go test ./agentintel/...` PASS。

## 2. 前端 (Vue)

### `api/uploads.ts`

- `fetchUploads()` → `GET /uploads`，返回 `UploadItem[]` `{id,kind,name,size,mtimeMs,sessionId,sessionName,cwd,url}`。
- `fetchInputs()` → `GET /inputs`，返回 `InputItem[]` `{text,tsMs,source,cwd,project,sessionName?}`。
- `rawUrl(id)` → `…/uploads/raw?id=<id>` (+ `?auth=<code>`，供 `<img>/<a>` header 无法携带)。

### 组件 `components/terminal-session/ResourceDrawer.vue`

Teleport 到 body。三标签：**图片 / 文件 / 输入**，顶部一条 **filter bar**。

- **图片**：2 列缩略图网格 (`<img loading="lazy">`)，点击 → lightbox。每项显示 name +
  **会话 chip** (`sessionName`，hover 显示 cwd) + 相对时间。
- **文件**：列表，类型字形 (IMG/PDF/TXT/BIN 配色)，name + 会话 chip + size + 相对时间；
  预览 (图片走 lightbox / 文本走新标签) · 下载 (raw `download`) · 复制文件名。
- **输入**：`fetchInputs()` 的跨会话 transcript 提示词 (**不再读 ComposeBar history store**)。
  每项 **source badge** (claude 紫 / codex 绿) + project + 相对时间；2 行 clamp，点开展开；
  「复制」(clipboard) + 「重发」(emit `send` → 父级 `onComposeSend`)。

### Filter bar (v6 范式)

一行三控件，桌面/移动通用：

1. **scope 下拉** — 图片/文件 tab 按 `sessionName` 去重列表 + 「全部会话」；输入 tab 按
   `source` (claude/codex) + 「全部来源」。切 tab 自动清掉不再适用的 scope。
2. **排序** — `最新 ↓` / `最早 ↑` 切换 (默认最新在前)。
3. **搜索** — 即时 substring 过滤 (图片/文件按 name+sessionName；输入按 text+project)。

tab 计数徽标显示**未过滤总量** (稳定库存)；过滤后空集显示「无匹配…」与「暂无…」区分。

### 刷新 (一处钩子)

上传成功后 `window` CustomEvent `dw:upload-success` (所有上传的唯一汇聚点)。抽屉监听，
仅在 open 时 refetch (跨会话列表无需 sessionId 匹配)。每次 open 也 refetch (uploads + inputs 并发)。

### 挂载点

`pages/TerminalPage.vue` 与 `components/terminal-session/CliTerminalSurface.vue` 各挂一处：
`<ResourceDrawer :session-id v-model:open @send="onComposeSend" />`。`session-id` **不再用于拉取数据**
(数据已全局)，仅作 **重发目标会话**。重发复用 canonical send path (`onComposeSend`)，抽屉不持第二个 WS。
与 TmuxQuickBar/TmuxPaneBar/Toolbar/InstallNotifyIcon 共存不冲突。

### 响应式

- **桌面 (`!isMobile`)**：右侧可收缩列 (320px 满高)；收起时右屏缘 slim 把手 (`.rd-handle`)
  召出；scrim 透明 + `pointer-events:none` (点终端不被遮挡)。
- **移动 (≤768px)**：右侧滑出 sheet (330px / `max calc(100vw-24px)`)，半透明 scrim，
  `@click.self` 关闭，`env(safe-area-inset-bottom)` 适配，触控目标 ≥24px。
- 过渡复用 TmuxStatusSheet：opacity 0.18s + panel `translateX(24px)` 0.2s。

## 3. 验证

- 后端：`GOPROXY=… go build ./...` exit 0 · `go test .` PASS · `go test ./agentintel/...` PASS。
- 前端：`npm run type-check` — 改动文件 **0 错误** (仅 11 个既有错误，位于
  `plugin.ts`/`portals/settings`/`../../deepwork/frontend`)；`npm run build` exit 0。
- 运行时 (:8093)：`GET /api/inputs` → HTTP 200，200 条人类提示词 (claude 159 + codex 41)，
  **0 条含合成标记** (`tool_result`/`<command-*>`/`AGENTS.md`/interrupt 全被排除)；
  `GET /api/uploads` → HTTP 200 `{items:[]}` (本机无上传，空数组而非报错)，`?kind=` 过滤生效。

## 4. SSOT / 不变量

- 上传**唯一真相** = `~/.dw-terminal/uploads.json` 索引；raw 服务**只**经 id 白名单，无客户端路径。
- 输入**唯一真相** = claude/codex transcript；抽屉只读解析，不在前端复制存储。
- 上传刷新只有**一个**事件源 (`dw:upload-success`)；发送只有**一条**路径 (`onComposeSend`)。
- AI/工具/合成消息在后端 `claudeRowToInput`/`collectCodexInputs` 统一剔除，前端不做二次过滤。
