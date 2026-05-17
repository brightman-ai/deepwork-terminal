# Cloudflare Tunnel

deepwork-terminal can automatically expose itself over a public HTTPS URL using [Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/) (via `cloudflared`).

## How it works

On startup, if tunnel is enabled:
1. The binary downloads `cloudflared` for your platform (cached in `~/.dw/cloudflared`).
2. Launches `cloudflared tunnel --url http://localhost:<port>`.
3. Prints the public URL to stdout.

No Cloudflare account or configuration is required for the free ephemeral tunnel.

## Enable via code

```go
cfg := terminal.DefaultConfig()
cfg.Tunnel = terminal.TunnelConfig{
    Enabled: true,
}
srv := terminal.New(cfg)
```

## Enable via CLI flag

```bash
dw-terminal --tunnel
```

## Notes

- The public URL changes on each restart (ephemeral tunnel).
- For a stable URL, set up a named tunnel with your Cloudflare account and pass the token via `cfg.Tunnel.Token`.
- Tunnel traffic is encrypted end-to-end by Cloudflare.
