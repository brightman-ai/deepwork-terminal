# deepwork-terminal

[English](README.md) | **简体中文**

一个**移动端优先的 web 终端，用来盯住和操控你的 AI 编码 agent**（Claude Code / Codex）——从任何地方够到 agent 真正运行的那台机器：路上用**手机**，或在远程机器上用**一个浏览器标签页代替 SSH 会话**。会话跑在 tmux 里，断线也不丢。内置鉴权、一个 flag 开公网的 Cloudflare 隧道、微信 / 飞书 / Web Push 通知，以及嵌入式 Vue 前端。一行 `http.Handler` 即可挂进任意 Go 应用。

## 为什么用

你在 Claude Code 里启动一个长耗时的重构，然后合上电脑。出门路上，它卡在一个 `Proceed? [Y/n]` 上——剩下的时间全白等。**agent 异步跑在一台机器上，而你不会守在那台机器前。**

deepwork-terminal 把终端"解钉"：把 agent 状态、产出文件、那个 `[Y/n]` 提示搬进任意浏览器，**会话跨断线存续**，并在 **agent 需要你或跑完的那一刻，用微信 / 飞书 / Web Push 找到你**。

## 给谁用

三个它真正值回票价的场景：

**1 · 人一走开，agent 不该干等。**
你在电脑上跑着 Claude Code / Codex，转身去开会、出门。agent 撞到 `[Y/n]` 或跑完时，一条推送——**微信 / 飞书 / 浏览器 Web Push**——找到你；点一下深链直达那个会话，回一句就行。输入区就是普通输入框，多行文本和手机**语音输入**都能直接发给 agent。*agent 异步，人在移动。*

**2 · 抗断网的远程开发。**
会话跑在开发机的 **tmux** 里，所以**进程长期存续**——锁屏、切 App、掉 wifi、换网络；重连即回到原样。两小时的 agent run 不会因为一次网络抖动全丢。就是 `tmux`/`screen` 那套"detach 也不断"，但配一个你真会想用的网页/移动 UI——用内置 Cloudflare 隧道或你自己的 Tailscale，随处 HTTPS 够到。

**3 · tmux 重度用户，多面板并行。**
同时开着好几个 agent。deepwork-terminal 给 tmux 配了极好用的触屏/网页操作层：一排快捷键条（复制 / 分屏 / 切 pane / 新建会话，实时显示 prefix——什么都不用背）、面板总览、一键切窗。在多个 agent 间切来切去，顺到你不想再回裸终端。

**贯穿三个场景——SSH 级文件收发，免 `scp`。**
在浏览器里复制、上传、下载、浏览 agent 工作目录里的文件：`Ctrl/Cmd+V` 把截图直接贴进 cwd（相对路径自动注入）、从手机相册上传、大文件**分片可续传**（穿弱网或隧道）、一键下载，以及一棵 VS Code 式目录树（新建 / 改名 / 删除、模糊搜索、浏览器内预览）。SSH 下最别扭的那几步，这里一步到位。

**不适合**只在一台机器、一个窗口里安静写代码、从不远程的人——你真的不需要它。

## 特性

**盯住并操控 AI 编码 agent**
- 实时 agent 状态（运行 / 等待 / 空闲），支持 Claude Code 与 Codex，含每会话的 token 与成本总览
- agent 需要你时通过 **Web Push（PWA）** 和 **微信（iLink 官方通道）** 通知——点一下深链回到对应会话
- 多端接管——任务进行中在手机与 PC 之间随时切换

**移动端优先的终端**
- 基于 WebSocket 的完整 PTY 终端，支持断线重连
- tmux 快捷键盘工具条（copy 模式、分屏 / 缩放 / 切 pane、新建 / 列出 / detach 会话）——无需记忆快捷键，动态显示前缀
- `Ctrl/Cmd+V` 粘贴截图 → 落到 active pane 的 cwd，路径注入终端；手机上传、一键下载同样支持
- 资源抽屉：跨会话上传索引、输入历史复用、文件模糊搜索，以及在浏览器里预览 agent 产出——markdown、代码、文本/日志、HTML 报告（源码 ⇄ 渲染切换）

**随处部署**
- 嵌入式 Vue SPA——无需伺候静态资源；以单个二进制分发
- 一个 flag 开公网——`--tunnel` 启动即开 Cloudflare quick 隧道（自动下载 `cloudflared`，无需账号）；固定域名的 named 隧道走 UI
- 为鉴权、会话生命周期、shell 定制提供 hook 点

## 安装

**AI-native——直接吩咐你的编码 agent。** 你本来就开着 Claude Code 或 Codex，一句话把整件事交给它：

> *「从 github.com/brightman-ai/deepwork-terminal 安装 deepwork-terminal——我的平台有预编译包就拿现成的，没有就从源码构建。跑在 8222 端口，然后问我要不要用 `--tunnel` 开公网访问。」*

agent 会自己下载（或构建）、启动，并从启动输出里把访问 URL 和登录码读回给你。因为开公网只是一个 `--tunnel` flag，「现在让我手机也能连」再追加一句就行——不用面板，不用配置文件。

想自己动手？预编译二进制，**不需要 Go 或 Node**：

```bash
curl -fsSL https://raw.githubusercontent.com/brightman-ai/deepwork-terminal/main/install.sh | sh
```

为 **Linux**（amd64/arm64）和 **macOS**（通用二进制）把 `dw-terminal` 装到 `~/.local/bin`。在 **WSL** 上这条路即可，开箱即用。指定版本或目录：

```bash
curl -fsSL https://raw.githubusercontent.com/brightman-ai/deepwork-terminal/main/install.sh | sh -s -- --version=v0.5.1 --dir=/usr/local/bin
```

### Homebrew（macOS / Linux）

```bash
brew install brightman-ai/tap/dw-terminal
```

### Go（开发者，Go ≥ 1.26）

```bash
go install github.com/brightman-ai/deepwork-terminal/cmd/dw-terminal@latest
```

> **网络慢或被墙？** 设个代理让模块 + Go 工具链下载成功
> （`./build.sh` 和 `install.sh --from-source` 已默认这么做）：
> ```bash
> GOPROXY=https://goproxy.cn,direct go install github.com/brightman-ai/deepwork-terminal/cmd/dw-terminal@latest
> ```

没装 Go 但想从源码构建？安装脚本可以帮你装最新稳定版 Go：

```bash
curl -fsSL https://raw.githubusercontent.com/brightman-ai/deepwork-terminal/main/install.sh | sh -s -- --from-source --install-go
```

### 手动下载

到 [Releases 页面](https://github.com/brightman-ai/deepwork-terminal/releases) 下载 tarball，把 `dw-terminal` 放进 `PATH`。

### 验证与运行

```bash
dw-terminal --version
dw-terminal --addr :8222              # 跑在 8222 端口（局域网可达）
dw-terminal --addr :8222 --tunnel     # ……并开公网访问
```

启动时 dw-terminal 会把可达的 URL（本地 + 局域网，带 `--tunnel` 时还有公网 `*.trycloudflare.com` 地址）和登录码打到 **stdout**——所以驱动它的 agent 能直接读回来。全部 flag 见 `dw-terminal --help`。

**平台说明**

- **Linux / WSL** — 一个 Linux 二进制通吃。WSL2 下，Windows 浏览器通过 `http://localhost:<port>` 访问。
- **macOS** — release 二进制以 Developer ID 签名并公证，Gatekeeper 放行。（Homebrew 和安装脚本也会清除 quarantine 标记作兜底。）

## 截图

### 标准界面 — tmux 分屏 + 快捷工具条

![标准界面：tmux 分屏 pane + 顶部快捷工具条](screenshots/ui-standard-tmux-panes.png)

---

### 粘贴、输入、或语音说给 agent

`Ctrl/Cmd+V` 粘贴截图，相对路径自动注入；输入区本质是普通输入框，多行文本和手机语音输入法也能直接发给 agent。

![多行文本输入](screenshots/input-multiline.png)

![语音输入法直接在输入框里用](screenshots/input-voice.png)

---

### 在手机上盯住 agent — 多通道通知

把「agent 在等你」推到你本来就在看的 IM——个人微信 / 飞书 / 企业微信 / 钉钉。

![多通道推送](screenshots/notify-channels.png)

个人微信（iLink）配额：每主动发一条消息约换 10 条推送，回任意字符即续。

![个人微信通知](screenshots/notify-wechat-1.png)

![个人微信通知](screenshots/notify-wechat-2.png)

![个人微信通知 — 配额续订](screenshots/notify-wechat-3.png)

![个人微信通知 — 已续订](screenshots/notify-wechat-4.png)

顶部状态条常驻：每个会话的状态 + 连接延迟。

![健康 / 延迟状态条](screenshots/health-latency-bar.png)

---

### 不用记 tmux 快捷键

两条工具条——一条管 tmux（pane / 会话），一条通用，并实时显示当前前缀。没装 tmux？照样能开多个终端。

![两条工具条：tmux + 通用](screenshots/toolbar-two-rows.png)

![不用 tmux 也能多终端](screenshots/multi-terminal-no-tmux.png)

---

### 跨会话连贯 — 上传 / 输入历史 / 文件抽屉

传过的文件、真输入过的 prompt 在任意新会话都能翻到；文件抽屉可在浏览器里浏览目录树、预览文件。

![历史：传过的图片 / 输入过的 prompt](screenshots/history-uploads.png)

![文件抽屉：目录树](screenshots/file-tree-browse.png)

![文件抽屉：文件预览](screenshots/file-preview.png)

---

### 还有这些

每会话 token 与成本总览；手机 / PC 共享同一会话，可抢占接管。

![会话成本总览](screenshots/session-cost.png)

![多端接管](screenshots/multi-device-takeover.png)

## 🔔 通知配置

当 agent 需要你（卡在 `[Y/n]`、或跑完）时，deepwork-terminal 会推送到任意已开启的通道：

| 通道 | 默认 | 凭据 |
|------|:----:|------|
| 微信（iLink） | 开 | 扫码登录 |
| 浏览器 Web Push（PWA） | 开 | 授权通知 |
| 飞书 / Lark | 关 | webhook URL + Secret（加签） |
| 钉钉 | 关 | webhook URL + Secret（加签） |
| 企业微信 | 关 | webhook URL（key 在 URL 内） |
| Slack | 关 | webhook URL |

全部在 **设置 → Notifications** 里配置。每个通道有 **开关**、**配置 Webhook** 表单（URL + 可选 Secret）、**测试** 按钮（发**真**消息并回显诚实结果）。URL/Secret 加密存储、UI 脱敏（Secret 只写不回显）。

各家 webhook URL 怎么拿：

### 飞书 / Lark
1. 在群里：**群设置 → 群机器人 → 添加机器人 → 自定义机器人**。
2. 安全设置选 **「签名校验」** → 复制 **Secret**。
3. 复制 **Webhook 地址**（`https://open.feishu.cn/open-apis/bot/v2/hook/…`）。
4. 应用内 → **配置 Webhook** → 填 **URL + Secret** → **保存** → 开开关 → **测试**。
   - 签名错返回 `19021` → 检查 Secret。（若选的是「关键词」模式，消息须含该词——**建议用签名校验**。）

### 🔔 钉钉
1. 群 → **群设置 → 智能群助手 → 添加机器人 → 自定义**。
2. 安全设置选 **「加签」** → 复制 secret（`SEC` 开头）。
3. 复制 Webhook 地址（`https://oapi.dingtalk.com/robot/send?access_token=…`）。
4. 应用内 → 填 **URL + Secret（加签）** → **保存** → 开 → **测试**。
   - 签名错 → `310000 sign not match`。**别用「关键词」模式**（消息可能被拒）。

### 企业微信
1. 群 → **右上「…」→ 群机器人 → 添加 → 创建**。
2. 复制 Webhook 地址（`https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=…`）。URL 里的 `key` 即凭据 —— **无需 Secret**。
3. 应用内 → 填 **URL**（Secret 留空）→ **保存** → 开 → **测试**。
   - `93000` → URL/key 错，或机器人已被移出群。

### Slack
1. 创建 incoming webhook：**api.slack.com/apps → Create New App → Incoming Webhooks → Activate → Add New Webhook to Workspace**，选一个频道。
2. 复制 Webhook 地址（`https://hooks.slack.com/services/…`）。URL 即凭据 —— **无需 Secret**。
3. 应用内 → 填 **URL**（Secret 留空）→ **保存** → 开 → **测试**。
   - 成功返回 `ok`；请求格式错返回 `invalid_payload`。服务端需能访问 `hooks.slack.com`（部分地区需代理）。

## 局限（如实说）

只报喜的项目不值得信，几条边界摆在前面：

- **没有原生 App** — Web 优先（PWA），不是应用商店里的应用；iOS Web Push 有平台门槛。
- **早期项目** — star 还少，生态 / 文档 / 社区都在早期。
- **纯本地** — 它是 agent 所在机器的遥控器，不是云端 agent 调度中心。但可经内置 Cloudflare Tunnel 开 HTTPS，任意网络访问。

## 作为库使用（Quick Start）

```bash
go get github.com/brightman-ai/deepwork-terminal
```

```go
import terminal "github.com/brightman-ai/deepwork-terminal"

srv := terminal.New(terminal.DefaultConfig())
http.Handle("/terminal/", srv.Handler())
http.ListenAndServe(":8080", nil)
```

或直接跑 CLI：

```bash
go run ./cmd/dw-terminal
```

完整文档见 [guide/](guide/)。

## 从源码构建

### 首次构建或更新到最新（推荐）

```bash
git clone https://github.com/brightman-ai/deepwork-terminal
cd deepwork-terminal
./build.sh
```

`build.sh` 一步搞定：

1. 克隆（或拉取到最新）CE App Shell（`brightman-ai/deepwork`）到同级目录
2. 跑 `npm install` 拉取新的前端依赖
3. 构建 Vue 前端并嵌入 `internal/spa/dist/`
4. 下载新的 Go 模块依赖（`go mod download`）
5. 编译 Go 二进制 → `./dw-terminal`

初次克隆后，更新到最新：

```bash
git pull
./build.sh
```

要求：Go 1.26+、Node.js 18+、npm。

> **无头服务器**：`npm install` 的浏览器下载 hook（Playwright/Puppeteer）会通过
> `PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1` 自动抑制。

### 仅 Go 二进制（不改前端）

前端是**预编译**并提交进 `internal/spa/dist/` 的。
若只需重新编译 Go 二进制：

```bash
./build.sh --skip-frontend
```

或手动：

```bash
go build -o dw-terminal ./cmd/dw-terminal/
```

## 参与贡献

开源（MIT）。如果你重度跑 Claude Code / Codex、又经常离开工位，欢迎试试——点个 ⭐ 或提个 issue 都很欢迎。

## 许可证

[MIT](LICENSE)
