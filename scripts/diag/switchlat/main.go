// switchlat — how long does ONE tmux window switch take, and where does the time go?
//
// The question this answers is not "is the server slow" but "why is the web terminal slower than
// attaching to the same tmux from a native terminal". Same tmux server, same windows, same
// keystroke; two transports. Everything that differs is in between.
//
//	go run ./scripts/diag/switchlat -session dwswitch
//
// Both paths are driven identically: send `prefix N`, then watch the PTY until it goes quiet.
// Reported per switch:
//
//	first-byte  — keystroke → the first byte of the redraw. Round-trip through whatever sits
//	              between the key and tmux; the part a user feels as "did it register?".
//	settle      — keystroke → output stops. The full repaint delivered.
//	bytes       — how much tmux emitted for that switch. Identical work on both paths, so a
//	              difference here means a transport is adding or duplicating traffic.
//
// native uses a plain PTY running `tmux attach` (what iTerm does). web goes through the running
// dw-terminal: create a session, attach its WebSocket, exactly like the browser.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/creack/pty"
)

// quietFor is how long the stream must be silent before a repaint counts as finished. Well above
// the observed inter-frame gap of a redraw burst (~130ms p95) so a lull mid-repaint is not read as
// completion, and well under the gap between switches.
const quietFor = 350 * time.Millisecond

// chunk is one arrival of output: when it landed and how big it was. Package-level so both
// transports feed the SAME collector — a comparison whose two sides are measured by two functions
// is a comparison of the functions.
type chunk struct {
	at time.Time
	n  int
}

type sample struct {
	firstByte time.Duration
	settle    time.Duration
	bytes     int
}

func report(label string, samples []sample) {
	if len(samples) == 0 {
		fmt.Printf("%-8s no samples\n", label)
		return
	}
	fb := make([]time.Duration, len(samples))
	st := make([]time.Duration, len(samples))
	total := 0
	for i, s := range samples {
		fb[i], st[i] = s.firstByte, s.settle
		total += s.bytes
	}
	sort.Slice(fb, func(a, b int) bool { return fb[a] < fb[b] })
	sort.Slice(st, func(a, b int) bool { return st[a] < st[b] })
	fmt.Printf("%-8s n=%d  first-byte p50=%-8v max=%-8v | settle p50=%-8v max=%-8v | avg %d B/switch\n",
		label, len(samples),
		fb[len(fb)/2].Round(time.Millisecond), fb[len(fb)-1].Round(time.Millisecond),
		st[len(st)/2].Round(time.Millisecond), st[len(st)-1].Round(time.Millisecond),
		total/len(samples))
}

// prefixSeq is C-b followed by the window digit — the exact bytes a user's keyboard produces.
func prefixSeq(win int) []byte { return []byte{0x02, byte('0' + win)} }

func nativeRun(session string, switches int) []sample {
	cmd := exec.Command("tmux", "attach", "-t", session)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 200, Rows: 50})
	if err != nil {
		fmt.Println("native attach failed:", err)
		return nil
	}
	defer func() { _ = ptmx.Close(); _ = cmd.Process.Kill() }()

	chunks := make(chan chunk, 4096)
	go func() {
		buf := make([]byte, 64*1024)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				chunks <- chunk{time.Now(), n}
			}
			if err != nil {
				close(chunks)
				return
			}
		}
	}()
	drain(chunks, 1500*time.Millisecond) // let the initial attach repaint finish

	var out []sample
	for i := 0; i < switches; i++ {
		win := 1 + i%4
		sent := time.Now()
		_, _ = ptmx.Write(prefixSeq(win))
		out = append(out, collect(chunks, sent))
	}
	return out
}

func webRun(addr, auth, session string, switches int) []sample {
	base := "http://" + addr + "/api"
	body := strings.NewReader(fmt.Sprintf(`{"shell":%q,"cwd":"/tmp"}`, "tmux attach -t "+session))
	req, _ := http.NewRequest("POST", base+"/sessions", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CLI-Auth", auth)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Println("create failed:", err)
		return nil
	}
	var created map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()
	id, _ := created["id"].(string)
	if id == "" {
		fmt.Println("create failed: no id")
		return nil
	}
	defer func() {
		dreq, _ := http.NewRequest("DELETE", base+"/sessions/"+id, nil)
		dreq.Header.Set("X-CLI-Auth", auth)
		if r, e := http.DefaultClient.Do(dreq); e == nil {
			r.Body.Close()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, fmt.Sprintf("ws://%s/api/sessions/%s/ws?auth=%s", addr, id, auth), nil)
	if err != nil {
		fmt.Println("dial failed:", err)
		return nil
	}
	defer conn.CloseNow()
	conn.SetReadLimit(64 << 20)

	chunks := make(chan chunk, 4096)
	go func() {
		for {
			typ, data, err := conn.Read(ctx)
			if err != nil {
				close(chunks)
				return
			}
			if typ == websocket.MessageBinary {
				chunks <- chunk{time.Now(), len(data)}
			}
		}
	}()
	drain(chunks, 2500*time.Millisecond) // attach repaint + the replay buffer

	var out []sample
	for i := 0; i < switches; i++ {
		win := 1 + i%4
		sent := time.Now()
		wctx, wcancel := context.WithTimeout(ctx, 2*time.Second)
		_ = conn.Write(wctx, websocket.MessageBinary, prefixSeq(win))
		wcancel()
		out = append(out, collect(chunks, sent))
	}
	return out
}

// collect measures one switch: time to the first byte of the redraw, time until the stream goes
// quiet, and the bytes tmux emitted.
//
// "Settled" is defined by silence rather than by a byte count, because the byte count is exactly
// what is under investigation — a transport that sends the same screen twice would otherwise be
// scored as finishing early.
func collect(ch <-chan chunk, sent time.Time) sample {
	var s sample
	timer := time.NewTimer(2 * time.Second) // nothing at all within 2s = a switch that did not happen
	defer timer.Stop()
	first := true
	for {
		select {
		case c, ok := <-ch:
			if !ok {
				return s
			}
			if first {
				s.firstByte = c.at.Sub(sent)
				first = false
			}
			s.settle = c.at.Sub(sent)
			s.bytes += c.n
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(quietFor)
		case <-timer.C:
			return s
		}
	}
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8087", "dw-terminal addr")
	auth := flag.String("auth", "perftest123", "auth code")
	session := flag.String("session", "dwswitch", "tmux session to switch windows in")
	switches := flag.Int("switches", 12, "how many window switches per path")
	flag.Parse()

	fmt.Printf("tmux session %q, %d switches per path, 200x50\n\n", *session, *switches)
	fmt.Println("native = plain PTY running `tmux attach` (what a native terminal does)")
	fmt.Println("web    = dw-terminal session + WebSocket (what the browser does)")
	fmt.Println()
	report("native", nativeRun(*session, *switches))
	report("web", webRun(*addr, *auth, *session, *switches))
}

// drain swallows everything currently flowing, until the stream has been quiet for `quiet`.
// Used to skip the attach repaint so it is not attributed to the first switch.
func drain(ch <-chan chunk, quiet time.Duration) {
	timer := time.NewTimer(quiet)
	defer timer.Stop()
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
			if !timer.Stop() {
				<-timer.C
			}
			timer.Reset(quiet)
		case <-timer.C:
			return
		}
	}
}
