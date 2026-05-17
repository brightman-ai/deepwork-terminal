# API Reference

## HTTP Endpoints

All endpoints are served under the path prefix passed to `srv.Handler()`.

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/` | Redirect to terminal UI |
| `GET` | `/ui/` | Embedded Vue SPA |
| `GET` | `/ws` | WebSocket — PTY stream |
| `POST` | `/api/session` | Create a new terminal session |
| `DELETE` | `/api/session/:id` | Close a session |
| `GET` | `/api/sessions` | List active sessions |
| `GET` | `/api/settings` | Get workbench settings |
| `PUT` | `/api/settings` | Update workbench settings |

## WebSocket Protocol

Connect to `/ws?session=<id>` to attach to a PTY session.

**Client → Server messages** (JSON):
```json
{ "type": "input", "data": "ls -la\n" }
{ "type": "resize", "cols": 120, "rows": 40 }
```

**Server → Client messages** (JSON):
```json
{ "type": "output", "data": "..." }
{ "type": "exit",   "code": 0 }
```

## Hooks

Implement `terminal.Hooks` to integrate with your application:

```go
type Hooks interface {
    // OnAuth is called before upgrading to WebSocket.
    // Return an error to reject the connection.
    OnAuth(r *http.Request) error

    // OnSessionStart is called when a new PTY session starts.
    OnSessionStart(sessionID string, r *http.Request)

    // OnSessionEnd is called when a PTY session exits.
    OnSessionEnd(sessionID string, exitCode int)

    // ShellEnv returns additional environment variables for the shell.
    ShellEnv(r *http.Request) []string
}
```

Pass hooks to the server:

```go
srv := terminal.New(cfg, terminal.WithHooks(myHooks{}))
```
