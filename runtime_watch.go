package terminal

import (
	"context"
	"net/http"
	"runtime"
	"sync"
	"time"
)

const (
	runtimeWatchInterval = time.Second
	runtimeStallDelay    = 2 * time.Second
	runtimeMemSample     = time.Minute
)

// runtimeWatch is the one owner of process-runtime diagnostics.  The ticker deliberately
// does not call ReadMemStats on every tick: collecting the full heap counters is more work than
// the cheap scheduler-latency probe, and the values are useful at minute resolution except when
// a stall is being diagnosed.
type runtimeWatch struct {
	startedAt time.Time

	mu    sync.RWMutex
	stats runtimeStats
	read  func(*runtime.MemStats)
}

type runtimeStats struct {
	Goroutines   int
	HeapAlloc    uint64
	HeapInuse    uint64
	HeapSys      uint64
	HeapObjects  uint64
	GCCount      uint32
	GCPauseTotal uint64
	GCLastPause  uint64
}

type runtimeHeapResponse struct {
	AllocBytes uint64 `json:"alloc_bytes"`
	InuseBytes uint64 `json:"inuse_bytes"`
	SysBytes   uint64 `json:"sys_bytes"`
	Objects    uint64 `json:"objects"`
}

type runtimeGCResponse struct {
	Count        uint32 `json:"count"`
	PauseTotalNs uint64 `json:"pause_total_ns"`
	LastPauseNs  uint64 `json:"last_pause_ns"`
}

type runtimeDebugResponse struct {
	Goroutines    int                 `json:"goroutines"`
	Heap          runtimeHeapResponse `json:"heap"`
	GC            runtimeGCResponse   `json:"gc"`
	UptimeSeconds float64             `json:"uptime_seconds"`
}

func newRuntimeWatch(startedAt time.Time) *runtimeWatch {
	w := &runtimeWatch{startedAt: startedAt, read: runtime.ReadMemStats}
	// Bootstrap the cache once. Subsequent reads happen only on the minute sample or while
	// diagnosing a stalled ticker, so the endpoint remains cheap and deterministic.
	w.sample()
	return w
}

func (w *runtimeWatch) sample() runtimeStats {
	var ms runtime.MemStats
	w.read(&ms)
	s := runtimeStats{
		Goroutines:   runtime.NumGoroutine(),
		HeapAlloc:    ms.HeapAlloc,
		HeapInuse:    ms.HeapInuse,
		HeapSys:      ms.HeapSys,
		HeapObjects:  ms.HeapObjects,
		GCCount:      ms.NumGC,
		GCPauseTotal: ms.PauseTotalNs,
	}
	if ms.NumGC > 0 {
		s.GCLastPause = ms.PauseNs[(ms.NumGC-1)%uint32(len(ms.PauseNs))]
	}
	w.mu.Lock()
	w.stats = s
	w.mu.Unlock()
	return s
}

func (w *runtimeWatch) current() runtimeStats {
	w.mu.RLock()
	s := w.stats
	w.mu.RUnlock()
	s.Goroutines = runtime.NumGoroutine()
	return s
}

func (w *runtimeWatch) response(now time.Time) runtimeDebugResponse {
	s := w.current()
	w.mu.RLock()
	started := w.startedAt
	w.mu.RUnlock()
	return runtimeDebugResponse{
		Goroutines: s.Goroutines,
		Heap: runtimeHeapResponse{
			AllocBytes: s.HeapAlloc,
			InuseBytes: s.HeapInuse,
			SysBytes:   s.HeapSys,
			Objects:    s.HeapObjects,
		},
		GC: runtimeGCResponse{
			Count:        s.GCCount,
			PauseTotalNs: s.GCPauseTotal,
			LastPauseNs:  s.GCLastPause,
		},
		UptimeSeconds: max(0, now.Sub(started).Seconds()),
	}
}

func (w *runtimeWatch) start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(runtimeWatchInterval)
		defer ticker.Stop()
		lastTick := time.Now()
		lastSample := time.Now()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// Use receive time, not the timestamp carried by time.Ticker. When the
				// scheduler is stalled the carried value is already old by the time this
				// goroutine runs; measuring it would hide exactly the pause we need to report.
				now := time.Now()
				delay := runtimeTickDelay(lastTick, now)
				lastTick = now
				if delay >= runtimeStallDelay {
					stats := w.sample()
					logger.Warn("runtime ticker stalled",
						"delay_ms", delay.Milliseconds(),
						"goroutines", stats.Goroutines,
						"gc_count", stats.GCCount,
						"gc_pause_ms", stats.GCLastPause/uint64(time.Millisecond),
						"gc_pause_total_ms", stats.GCPauseTotal/uint64(time.Millisecond))
					lastSample = now
				} else if now.Sub(lastSample) >= runtimeMemSample {
					w.sample()
					lastSample = now
				}
			}
		}
	}()
}

// runtimeTickDelay returns scheduler delay beyond the ticker's one-second period. Keeping this
// pure makes the threshold boundary testable without sleeping in a test.
func runtimeTickDelay(previous, current time.Time) time.Duration {
	if previous.IsZero() {
		return 0
	}
	elapsed := current.Sub(previous)
	if elapsed <= runtimeWatchInterval {
		return 0
	}
	return elapsed - runtimeWatchInterval
}

// handleRuntimeDebug is intentionally read-only. It serves the authenticated GET
// /api/debug/runtime endpoint through the normal server mux.
func (s *Server) handleRuntimeDebug(w http.ResponseWriter, r *http.Request) {
	runtimeState := s.runtimeWatch
	if runtimeState == nil {
		runtimeState = newRuntimeWatch(time.Now())
	}
	writeJSON(w, http.StatusOK, runtimeState.response(time.Now()))
}
