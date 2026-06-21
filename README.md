# deepwork-terminal

A standalone web terminal with authentication, Cloudflare tunnel support, and an embedded Vue frontend. Drop it into any Go application via a single HTTP handler.

## Features

- Full PTY terminal over WebSocket
- Session management with reconnect support
- Clipboard paste support
- Settings portal (workbench API)
- Optional Cloudflare Tunnel (auto-downloads `cloudflared`)
- Embedded Vue SPA — zero static file serving required
- Hook points for auth, session lifecycle, and shell customization

## Install

Fastest path — a prebuilt binary, **no Go or Node required**:

```bash
curl -fsSL https://raw.githubusercontent.com/brightman-ai/deepwork-terminal/main/install.sh | sh
```

Installs `dw-terminal` to `~/.local/bin` for **Linux** (amd64/arm64) and **macOS** (universal). On **WSL**, this is the right path — it just works. Pin a version or change the directory:

```bash
curl -fsSL https://raw.githubusercontent.com/brightman-ai/deepwork-terminal/main/install.sh | sh -s -- --version=v0.3.0 --dir=/usr/local/bin
```

### Homebrew (macOS / Linux)

```bash
brew install brightman-ai/tap/dw-terminal
```

### Go (developers, Go ≥ 1.26)

```bash
go install github.com/brightman-ai/deepwork-terminal/cmd/dw-terminal@latest
```

> **Slow or blocked network?** Set a proxy so module + Go-toolchain downloads succeed
> (`./build.sh` and `install.sh --from-source` already default to this):
> ```bash
> GOPROXY=https://goproxy.cn,direct go install github.com/brightman-ai/deepwork-terminal/cmd/dw-terminal@latest
> ```

No Go installed but want a source build? The installer can bootstrap the latest stable Go:

```bash
curl -fsSL https://raw.githubusercontent.com/brightman-ai/deepwork-terminal/main/install.sh | sh -s -- --from-source --install-go
```

### Manual download

Grab a tarball from the [Releases page](https://github.com/brightman-ai/deepwork-terminal/releases) and put `dw-terminal` on your `PATH`.

### Verify & run

```bash
dw-terminal --version
dw-terminal --addr :8022
```

**Platform notes**

- **Linux / WSL** — one Linux binary covers both. On WSL2 a Windows browser reaches the server at `http://localhost:<port>`.
- **macOS** — release binaries are signed with a Developer ID and notarized, so Gatekeeper allows them. (Homebrew and the install script also clear the quarantine flag as a fallback.)

## Screenshots

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

## Quick Start (as a library)

```bash
go get github.com/brightman-ai/deepwork-terminal
```

```go
import terminal "github.com/brightman-ai/deepwork-terminal"

srv := terminal.New(terminal.DefaultConfig())
http.Handle("/terminal/", srv.Handler())
http.ListenAndServe(":8080", nil)
```

Or run the CLI directly:

```bash
go run ./cmd/dw-terminal
```

See [guide/](guide/) for full documentation.

## Build from source

### First time or update to latest (recommended)

```bash
git clone https://github.com/brightman-ai/deepwork-terminal
cd deepwork-terminal
./build.sh
```

`build.sh` handles everything in one step:

1. Clones (or pulls to latest) the CE App Shell (`brightman-ai/deepwork`) into a sibling directory
2. Runs `npm install` to pick up any new frontend dependencies
3. Builds the Vue frontend and embeds it into `internal/spa/dist/`
4. Downloads any new Go module dependencies (`go mod download`)
5. Compiles the Go binary → `./dw-terminal`

To update to the latest version after an initial clone:

```bash
git pull
./build.sh
```

Requires: Go 1.26+, Node.js 18+, npm.

> **Headless servers**: `npm install` browser-download hooks (Playwright/Puppeteer) are
> suppressed automatically via `PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1`.

### Go binary only (no frontend changes)

The frontend is **pre-built** and committed to `internal/spa/dist/`.
If you only need to recompile the Go binary:

```bash
./build.sh --skip-frontend
```

Or manually:

```bash
go build -o dw-terminal ./cmd/dw-terminal/
```

## License

[MIT](LICENSE)
