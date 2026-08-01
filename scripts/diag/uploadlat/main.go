// uploadlat — does a big upload steal the terminal?
//
// The question this answers: while a file is being uploaded into a session, does the tmux stream
// the user is actually looking at keep refreshing at its normal latency, or does it stall behind
// the transfer? Same shape as switchlat — drive `prefix N` window switches and measure keystroke →
// first byte and keystroke → settled — but with an upload deliberately running across the middle
// third of the run, so the SAME operation is measured quiet and contended.
//
//	# a tmux session with a few windows, and a dw-terminal that is NOT the one you are using
//	tmux new-session -d -s dwup && for i in 1 2 3; do tmux new-window -t dwup; done
//	go run ./scripts/diag/uploadlat -addr 127.0.0.1:8099 -auth <code> -session dwup -mb 300
//
// Report per phase (before / during / after), so the comparison is within one run against one
// server rather than across two runs that differ in machine load. `during` is the number that
// matters; `after` is the control that says the machine simply got busy for unrelated reasons.
//
// Run it against a SEPARATE instance, never the one a human is using — the point is to generate
// contention, and doing that to a live terminal is the mistake this file exists to measure.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// quietFor is how long the stream must be silent before a repaint counts as finished. Same value
// as switchlat so the two probes' numbers can be read side by side.
const quietFor = 350 * time.Millisecond

type chunk struct {
	at time.Time
	n  int
}

type sample struct {
	sent      time.Time
	firstByte time.Duration
	settle    time.Duration
	bytes     int
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8099", "dw-terminal addr (NOT the instance you are using)")
	auth := flag.String("auth", "verify8099", "auth code")
	session := flag.String("session", "dwup", "tmux session with several windows")
	rounds := flag.Int("rounds", 24, "window switches total; the upload runs across the middle third")
	mb := flag.Int("mb", 300, "upload size in MB")
	gap := flag.Duration("gap", 250*time.Millisecond, "pause between switches")
	flag.Parse()

	base := "http://" + *addr + "/api"
	watch, err := createSession(base, *auth, "tmux attach -t "+*session)
	if err != nil {
		fmt.Println("create watch session:", err)
		return
	}
	defer deleteSession(base, *auth, watch)
	// The upload lands in a DIFFERENT session, which is the realistic shape: one terminal is
	// streaming while a paste is absorbed elsewhere in the same process.
	sink, err := createSession(base, *auth, "/bin/sh")
	if err != nil {
		fmt.Println("create sink session:", err)
		return
	}
	defer deleteSession(base, *auth, sink)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, fmt.Sprintf("ws://%s/api/sessions/%s/ws?auth=%s", *addr, watch, *auth), nil)
	if err != nil {
		fmt.Println("dial:", err)
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(64 << 20)

	chunks := make(chan chunk, 8192)
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
	drain(chunks, 2500*time.Millisecond) // attach repaint + replay buffer

	var (
		mu                     sync.Mutex
		upStart, upEnd         time.Time
		upElapsed              time.Duration
		upStatus               int
		samples                []sample
		startAt                = *rounds / 3
		endAt                  = startAt + *rounds/3
		uploadDone             = make(chan struct{})
		uploadStarted, started bool
	)

	for i := 0; i < *rounds; i++ {
		if i == startAt && !started {
			started = true
			uploadStarted = true
			mu.Lock()
			upStart = time.Now()
			mu.Unlock()
			go func() {
				status, elapsed := postUpload(base, *auth, sink, *mb)
				mu.Lock()
				upStatus, upElapsed, upEnd = status, elapsed, time.Now()
				mu.Unlock()
				close(uploadDone)
			}()
		}
		if i == endAt && uploadStarted {
			// Let the transfer finish before the `after` phase starts, so a slow upload cannot
			// leak into the control group and make contention look like it ended early.
			<-uploadDone
		}
		win := 1 + i%4
		sent := time.Now()
		wctx, wcancel := context.WithTimeout(ctx, 2*time.Second)
		_ = conn.Write(wctx, websocket.MessageBinary, prefixSeq(win))
		wcancel()
		s := collect(chunks, sent)
		s.sent = sent
		samples = append(samples, s)
		time.Sleep(*gap)
	}
	if uploadStarted {
		<-uploadDone
	}

	mu.Lock()
	s, e, el, st := upStart, upEnd, upElapsed, upStatus
	mu.Unlock()

	var before, during, after []sample
	for _, smp := range samples {
		switch {
		case smp.sent.Before(s):
			before = append(before, smp)
		case smp.sent.Before(e):
			during = append(during, smp)
		default:
			after = append(after, smp)
		}
	}

	fmt.Printf("tmux session %q · %d switches · upload %d MB → HTTP %d in %v (%.0f MB/s)\n\n",
		*session, *rounds, *mb, st, el.Round(time.Millisecond), float64(*mb)/el.Seconds())
	report("before", before)
	report("during", during)
	report("after", after)
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
	fmt.Printf("%-8s n=%d  first-byte p50=%-8v p90=%-8v max=%-8v | settle p50=%-8v max=%-8v | avg %d B\n",
		label, len(samples),
		fb[len(fb)/2].Round(time.Millisecond), fb[(len(fb)*9)/10].Round(time.Millisecond), fb[len(fb)-1].Round(time.Millisecond),
		st[len(st)/2].Round(time.Millisecond), st[len(st)-1].Round(time.Millisecond),
		total/len(samples))
}

// prefixSeq is C-b followed by the window digit — the exact bytes a user's keyboard produces.
func prefixSeq(win int) []byte { return []byte{0x02, byte('0' + win)} }

func collect(ch <-chan chunk, sent time.Time) sample {
	var s sample
	timer := time.NewTimer(2 * time.Second)
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

func createSession(base, auth, shell string) (string, error) {
	body := strings.NewReader(fmt.Sprintf(`{"shell":%q,"cwd":"/tmp"}`, shell))
	req, _ := http.NewRequest("POST", base+"/sessions", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CLI-Auth", auth)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var created map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&created)
	id, _ := created["id"].(string)
	if id == "" {
		return "", fmt.Errorf("no id in create response")
	}
	return id, nil
}

func deleteSession(base, auth, id string) {
	req, _ := http.NewRequest("DELETE", base+"/sessions/"+id, nil)
	req.Header.Set("X-CLI-Auth", auth)
	if r, err := http.DefaultClient.Do(req); err == nil {
		r.Body.Close()
	}
}

// postUpload streams a generated payload through io.Pipe rather than building it in memory: a
// probe that allocates 300 MB to measure a server that no longer does would be measuring itself.
func postUpload(base, auth, sessionID string, mb int) (int, time.Duration) {
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		defer pw.Close()
		fw, err := mw.CreateFormFile("file", "uploadlat-payload.bin")
		if err != nil {
			return
		}
		block := bytes.Repeat([]byte("dwuploadlat"), 95325) // ~1 MiB of non-compressible-enough filler
		for i := 0; i < mb; i++ {
			if _, err := fw.Write(block); err != nil {
				return
			}
		}
		_ = mw.WriteField("mime", "application/octet-stream")
		_ = mw.WriteField("cwd", "/tmp/dwuploadlat")
		_ = mw.Close()
	}()

	req, _ := http.NewRequest("POST", base+"/sessions/"+sessionID+"/paste-upload", pr)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-CLI-Auth", auth)
	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, time.Since(start)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode, time.Since(start)
}
