# deepwork-terminal

[English](README.md) | **简体中文**

一个**移动端优先(移动笔记本/移动手机操控家里的台式机；手机 远程操控家里的笔记本）的 web 终端，用来在手机上盯住和操控你的 AI 编码 agent**（Claude Code / Codex）——内置鉴权、Cloudflare 隧道、Web Push / 微信通知，以及嵌入式 Vue 前端。一行 `http.Handler` 即可挂进任意 Go 应用。

## 为什么用

你在 Claude Code 里启动一个长耗时的重构，然后合上电脑。出门路上，它卡在一个 `Proceed? [Y/n]` 上——剩下的时间全白等。**agent 是异步的，人是移动的，但终端被钉死在那台机器上。**

deepwork-terminal 把那台机器上的终端——agent 状态、产出文件、`[Y/n]` 提示——搬到你手机的浏览器里，并在 agent 真正需要你时推送通知（Web Push **或** 微信）。点通知，直达对应会话，回一句，继续做你的事。

四个真正改变体验的细节：

1. **截图远程粘贴** — 在 PC 上 `Ctrl/Cmd+V` 粘贴截图，它会落到 agent 当前的工作目录，相对路径直接注入命令行；手机也能选文件 / 拍照上传。输入区本质就是普通输入框，所以**多行文本和手机语音输入法**也能直接发给 agent。
2. **手机盯 agent** — 装成 PWA 收 Web Push，点通知深链回到对应会话；微信 iLink 官方通道作备用；顶部常驻 agent 状态条；虚拟键盘自适应，永不挡住输入框。
3. **tmux 快捷键盘面板** — 一排按钮搞定 copy / 分屏 / 切 pane / 新建会话，并实时显示你当前的 tmux 前缀——不用记快捷键。没装 tmux？照样能开多个终端，只是少了分屏。
4. **跨会话连贯** — 全局上传索引、输入历史复用、文件抽屉（图片/文本预览 + 模糊搜索），都跟着你跨会话存在。

**给谁**：重度跑 agent、又经常离开工位的人。**不给谁**：只在本机一个窗口安静写代码、从不远程的人——你不需要它。

## 特性

**盯住并操控 AI 编码 agent**
- 实时 agent 状态（运行 / 等待 / 空闲），支持 Claude Code 与 Codex，含每会话的 token 与成本总览
- agent 需要你时通过 **Web Push（PWA）** 和 **微信（iLink 官方通道）** 通知——点一下深链回到对应会话
- 多端接管——任务进行中在手机与 PC 之间随时切换

**移动端优先的终端**
- 基于 WebSocket 的完整 PTY 终端，支持断线重连
- tmux 快捷键盘工具条（copy 模式、分屏 / 缩放 / 切 pane、新建 / 列出 / detach 会话）——无需记忆快捷键，动态显示前缀
- `Ctrl/Cmd+V` 粘贴截图 → 落到 active pane 的 cwd，路径注入终端；手机上传同样支持
- 资源抽屉：跨会话上传索引、输入历史复用、图片 / 文本预览与文件模糊搜索

**随处部署**
- 嵌入式 Vue SPA——无需伺候静态资源；以单个二进制分发
- 可选 Cloudflare Tunnel（自动下载 `cloudflared`）
- 为鉴权、会话生命周期、shell 定制提供 hook 点

## 安装

最省事——预编译二进制，**不需要 Go 或 Node**：

```bash
curl -fsSL https://raw.githubusercontent.com/brightman-ai/deepwork-terminal/main/install.sh | sh
```

为 **Linux**（amd64/arm64）和 **macOS**（通用二进制）把 `dw-terminal` 装到 `~/.local/bin`。在 **WSL** 上这条路即可，开箱即用。指定版本或目录：

```bash
curl -fsSL https://raw.githubusercontent.com/brightman-ai/deepwork-terminal/main/install.sh | sh -s -- --version=v0.3.2 --dir=/usr/local/bin
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
dw-terminal --addr :8022
```

**平台说明**

- **Linux / WSL** — 一个 Linux 二进制通吃。WSL2 下，Windows 浏览器通过 `http://localhost:<port>` 访问。
- **macOS** — release 二进制以 Developer ID 签名并公证，Gatekeeper 放行。（Homebrew 和安装脚本也会清除 quarantine 标记作兜底。）

## 截图

### 会话接管与抢占 — 手机 + PC 随时切换

多端共享同一终端会话，手机和 PC 可随时接管或抢占控制权，无缝远程切换操作。

![会话接管与抢占](screenshots/support-session-takeover.png)

---

### Textarea 文本输入 — 历史不丢失

内置多行文本输入框，发送历史持久保留，复杂命令编辑更方便，告别误触清空的烦恼。

![Textarea 输入](screenshots/support-textarea-input.png)

---

### 快捷键盘输入

针对移动端优化的快捷键盘面板，常用控制键一触即达（Ctrl、Esc、Tab、方向键等）。

![快捷键盘](screenshots/support-quick-keyboard.png)

---

### Snippets 片段管理 — 快捷输入

保存常用命令片段，点击即插入，减少重复输入，提升效率。

![Snippets 管理](screenshots/support-snippets.png)

---

### tmux 专项面板 — 快捷切换 Pane

内置 tmux 集成面板，直观展示所有 pane，一键切换，无需记忆 tmux 快捷键。

![tmux 面板](screenshots/support-tmux-panel.png)

---

### 截图 / 文件上传为图片 — PC 与移动端均支持

从 PC 浏览器或移动端上传截图和文件，自动转为图片链接，供 AI 工具（Codex / Claude）直接访问，快速排查问题。

![文件上传](screenshots/support-file-upload.png)

![移动端上传](screenshots/support-mobile-upload.png)

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
