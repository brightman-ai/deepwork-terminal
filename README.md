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

### Go binary only (recommended for servers)

The frontend is **pre-built** and committed to the repo (`internal/spa/dist/`).
Building the binary requires only Go — no Node.js needed:

```bash
git clone https://github.com/brightman-ai/deepwork-terminal
cd deepwork-terminal
go build -o dw-terminal ./cmd/dw-terminal/
```

### Full build (frontend + Go)

If you modify the Vue frontend source (`frontend/src/`), rebuild and re-embed:

```bash
# Requires Node.js 18+ and npm
./build.sh
```

`build.sh` runs `npm install` + `vite build`, copies the output to `internal/spa/dist/`,
then compiles the Go binary. The updated `internal/spa/dist/` should be committed
alongside your frontend changes so others can build without Node.js.

> **Headless servers**: if `npm install` triggers a browser download (Playwright/Puppeteer
> postinstall), the script sets `PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1` automatically.

## License

[MIT](LICENSE)
