// tmuxprobe — what does ONE status probe cost, and what happens when N connections each want one?
//
//	go run ./scripts/diag/tmuxprobe [shellPID]
//
// Lives in the repo rather than in a scratch directory because the numbers in tmux_state.go's and
// handlers.go's comments came from it: "0.6s-4.3s per probe", "8 concurrent callers 515ms -> 75ms".
// A claim like that rots the moment nobody can re-run it. Run it with the UI open and with it
// closed — the difference between those two IS the contention this design exists to remove.
//
// handlers.go used to run this probe inside the WS writer's select, so its latency landed straight
// on keystroke echo. It also ran once PER CONNECTION, against a tmux server that serves commands
// one at a time — so the probes queued behind each other and each one got slower as tabs were
// added. This measures both: single-caller cost, and N concurrent callers.
package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/brightman-ai/deepwork-terminal/agentintel"
)

func main() {
	shellPID := 0
	if len(os.Args) > 1 {
		shellPID, _ = strconv.Atoi(os.Args[1])
	}
	ctx := context.Background()
	svc := agentintel.NewTmuxStateService()

	fmt.Println("== single caller ==")
	for i := range 3 {
		start := time.Now()
		st := svc.State(ctx, shellPID)
		panes := 0
		for _, s := range st.Sessions {
			for _, w := range s.Windows {
				panes += len(w.Panes)
			}
		}
		fmt.Printf("  run %d: %9v  (panes=%d)\n", i+1, time.Since(start).Round(time.Millisecond), panes)
		time.Sleep(1100 * time.Millisecond) // past the memo TTL, so every run is a real rebuild
	}

	// N concurrent callers = N browser tabs / split panes on one machine, each with its own WS.
	for _, n := range []int{2, 4, 8} {
		var wg sync.WaitGroup
		durs := make([]time.Duration, n)
		start := time.Now()
		for i := range n {
			wg.Add(1)
			go func() {
				defer wg.Done()
				t := time.Now()
				svc.State(ctx, shellPID)
				durs[i] = time.Since(t)
			}()
		}
		wg.Wait()
		wall := time.Since(start)
		sort.Slice(durs, func(a, b int) bool { return durs[a] < durs[b] })
		fmt.Printf("== %d concurrent callers: wall=%v  fastest=%v  slowest=%v\n",
			n, wall.Round(time.Millisecond), durs[0].Round(time.Millisecond), durs[n-1].Round(time.Millisecond))
		time.Sleep(1100 * time.Millisecond)
	}
}
