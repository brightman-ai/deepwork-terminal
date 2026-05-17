# Getting Started

## Requirements

- Go 1.26+
- Node.js 18+ (only if rebuilding the frontend)

## Installation

```bash
go get github.com/brightman-ai/deepwork-terminal
```

## Embed in your application

```go
package main

import (
    "net/http"
    terminal "github.com/brightman-ai/deepwork-terminal"
)

func main() {
    cfg := terminal.DefaultConfig()
    cfg.Shell = "/bin/zsh"   // optional: override default shell

    srv := terminal.New(cfg)
    http.Handle("/terminal/", srv.Handler())
    http.ListenAndServe(":8080", nil)
}
```

Navigate to `http://localhost:8080/terminal/` to open the terminal UI.

## Run as standalone CLI

```bash
go install github.com/brightman-ai/deepwork-terminal/cmd/dw-terminal@latest
dw-terminal --port 8080
```

## Build from source

```bash
git clone https://github.com/brightman-ai/deepwork-terminal
cd deepwork-terminal
./build.sh   # builds frontend + Go binary
./run.sh     # starts the server
```

## Enable Cloudflare Tunnel

Set the tunnel option to get a public HTTPS URL without port forwarding:

```go
cfg.Tunnel = terminal.TunnelConfig{Enabled: true}
```

`cloudflared` is downloaded automatically on first use. See [tunnel.md](tunnel.md).
