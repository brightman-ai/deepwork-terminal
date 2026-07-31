// wsprobe — attach a session's WebSocket exactly like the browser does, and measure what the
// SERVER pushes at an IDLE terminal.
//
//	go run ./scripts/diag/wsprobe -shell "tmux attach -t <session>" -dur 12s [-ghost] [-echo-window 400ms]
//
// -ghost replays the frontend's alt-screen ghosting guard (CliTerminalSurface.scheduleGhostRefresh);
// -echo-window replays its v4 loop breaker. Running both ways is how the self-feeding loop was
// pinned down and how the fix was measured: 18.8 KB/s and 7.6 refresh-client/s on a QUIET pane
// before, 1.0 KB/s and 0.2/s after. Kept in the repo so that A/B is reproducible rather than
// remembered — see GHOST_ECHO_WINDOW in frontend/src/composables/cli/ghostRefresh.ts.
//
// Creates one session, attaches its WebSocket exactly like the browser does, and reports what the
// SERVER pushes: binary (PTY) bytes/frames, control frames by type, and the arrival-gap histogram
// that would show a writer goroutine stalled behind the 1s tick.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

var (
	ghostMu        sync.Mutex
	ghostTimer     *time.Timer
	ghostLastFired time.Time
	ghostEchoUntil time.Time
	ghostLastInput time.Time
	ghostCalls     int
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8087", "server addr")
	auth := flag.String("auth", "perftest123", "auth code")
	shell := flag.String("shell", "/bin/zsh", "shell for the session")
	dur := flag.Duration("dur", 15*time.Second, "observation window")
	ghost := flag.Bool("ghost", false, "replicate the frontend's scheduleGhostRefresh: POST /tmux/refresh, leading-edge throttled at 120ms, on every PTY output frame")
	typingQuiet := flag.Duration("typing-quiet", 0, "v5: an output frame does not arm a fire while a keystroke landed within this long (0 = off)")
	maxStale := flag.Duration("max-stale", 2500*time.Millisecond, "v5 ceiling: never defer a fire longer than this past the last one")
	typing := flag.Bool("typing", false, "send single keystrokes at human speed instead of one output burst")
	echoWindow := flag.Duration("echo-window", 0, "v4 loop breaker: ignore output frames arriving within this long after a fire (0 = pre-fix behaviour)")
	flag.Parse()

	base := "http://" + *addr + "/api"
	body := strings.NewReader(fmt.Sprintf(`{"shell":%q,"cwd":"/tmp"}`, *shell))
	req, _ := http.NewRequest("POST", base+"/sessions", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CLI-Auth", *auth)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Println("create failed:", err)
		os.Exit(1)
	}
	var created map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()
	id, _ := created["id"].(string)
	if id == "" {
		fmt.Printf("create failed: status=%d body=%v\n", resp.StatusCode, created)
		os.Exit(1)
	}
	fmt.Println("session:", id, "shell:", *shell)

	ctx, cancel := context.WithTimeout(context.Background(), *dur+10*time.Second)
	defer cancel()
	wsURL := fmt.Sprintf("ws://%s/api/sessions/%s/ws?auth=%s", *addr, id, *auth)
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		fmt.Println("dial failed:", err)
		os.Exit(1)
	}
	defer conn.CloseNow()
	conn.SetReadLimit(64 << 20)

	// Let the shell settle, then start counting.
	settle := time.Now().Add(3 * time.Second)
	var (
		binBytes, binFrames int
		ctrl                = map[string]int{}
		ctrlBytes           = map[string]int{}
		gaps                []time.Duration
		preface             string
		last                time.Time
		start               time.Time
	)
	// Two workloads. Default: one burst of fresh output (a screen change). -typing: single
	// keystrokes at human speed, which is the case being investigated — a keystroke echoes a few
	// bytes, and the question is what the client does in response to those few bytes.
	go func() {
		time.Sleep(4 * time.Second)
		if *typing {
			for i := 0; i < 30; i++ {
				wctx, wcancel := context.WithTimeout(ctx, 2*time.Second)
				ghostMu.Lock()
				ghostLastInput = time.Now()
				ghostMu.Unlock()
				_ = conn.Write(wctx, websocket.MessageBinary, []byte{byte('a' + i%26)})
				wcancel()
				time.Sleep(140 * time.Millisecond)
			}
			return
		}
		wctx, wcancel := context.WithTimeout(ctx, 2*time.Second)
		_ = conn.Write(wctx, websocket.MessageBinary, []byte("head -c 3000 /dev/urandom | base64 | head -40\r"))
		wcancel()
	}()

	deadline := time.Now().Add(*dur + 3*time.Second)
	for time.Now().Before(deadline) {
		rctx, rcancel := context.WithTimeout(ctx, 3*time.Second)
		typ, data, err := conn.Read(rctx)
		rcancel()
		if err != nil {
			break
		}
		now := time.Now()
		if now.Before(settle) {
			if len(preface) < 400 {
				preface += string(data)
			}
			continue
		}
		if start.IsZero() {
			start = now
			last = now
		}
		switch typ {
		case websocket.MessageBinary:
			binBytes += len(data)
			binFrames++
			gaps = append(gaps, now.Sub(last))
			last = now
			ghostMu.Lock()
			suppressed := *echoWindow > 0 && now.Before(ghostEchoUntil)
			// v5: a keystroke buys quiet, bounded by max-stale so typing cannot starve the fire.
			if *typingQuiet > 0 && !ghostLastInput.IsZero() && now.Sub(ghostLastInput) < *typingQuiet {
				// Mirrors ghostRefreshDeferredForTyping: a never-fired guard is infinitely stale,
				// so the first correction is never deferred.
				if !ghostLastFired.IsZero() && now.Sub(ghostLastFired) < *maxStale {
					suppressed = true
				}
			}
			armable := *ghost && ghostTimer == nil && !suppressed
			ghostMu.Unlock()
			if armable {
				wait := 120*time.Millisecond - time.Since(ghostLastFired)
				if wait < 0 {
					wait = 0
				}
				ghostTimer = time.AfterFunc(wait, func() {
					ghostMu.Lock()
					ghostTimer = nil
					ghostLastFired = time.Now()
					ghostEchoUntil = ghostLastFired.Add(*echoWindow)
					ghostCalls++
					ghostMu.Unlock()
					rr, _ := http.NewRequest("POST", base+"/tmux/refresh?session="+id, nil)
					rr.Header.Set("X-CLI-Auth", *auth)
					if rp, e := http.DefaultClient.Do(rr); e == nil {
						rp.Body.Close()
					}
				})
			}
		case websocket.MessageText:
			var m struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal(data, &m)
			ctrl[m.Type]++
			ctrlBytes[m.Type] += len(data)
		}
	}
	elapsed := dur.Seconds()
	fmt.Printf("\n-- first bytes after attach (settle window) --\n%q\n", preface)
	fmt.Printf("\n== observed %.1fs (idle shell, no user input) ==\n", elapsed)
	fmt.Printf("PTY binary : %d frames, %d bytes  → %.0f B/s\n", binFrames, binBytes, float64(binBytes)/elapsed)
	keys := make([]string, 0, len(ctrl))
	for k := range ctrl {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("control %-18s %3d frames, %7d bytes → %.0f B/s\n", k, ctrl[k], ctrlBytes[k], float64(ctrlBytes[k])/elapsed)
	}
	ghostMu.Lock()
	gc := ghostCalls
	ghostMu.Unlock()
	if *ghost {
		fmt.Printf("ghost refresh-client calls: %d → %.1f/s\n", gc, float64(gc)/elapsed)
	}
	if len(gaps) > 0 {
		sort.Slice(gaps, func(i, j int) bool { return gaps[i] < gaps[j] })
		fmt.Printf("binary arrival gap: p50=%v p95=%v max=%v\n",
			gaps[len(gaps)/2], gaps[len(gaps)*95/100], gaps[len(gaps)-1])
	}

	dreq, _ := http.NewRequest("DELETE", base+"/sessions/"+id, nil)
	dreq.Header.Set("X-CLI-Auth", *auth)
	if r, e := http.DefaultClient.Do(dreq); e == nil {
		r.Body.Close()
	}
}
