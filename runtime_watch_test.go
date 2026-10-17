package terminal

import (
	"encoding/json"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"
)

func TestRuntimeTickDelayUsesOneSecondBudget(t *testing.T) {
	base := time.Unix(100, 0)
	if got := runtimeTickDelay(base, base.Add(time.Second)); got != 0 {
		t.Fatalf("on-time tick delay = %s, want zero", got)
	}
	if got := runtimeTickDelay(base, base.Add(3*time.Second)); got != 2*time.Second {
		t.Fatalf("late tick delay = %s, want 2s", got)
	}
}

func TestRuntimeDebugResponseIsReadOnlySnapshot(t *testing.T) {
	started := time.Now().Add(-5 * time.Second)
	w := newRuntimeWatch(started)
	got := httptest.NewRecorder()
	s := &Server{runtimeWatch: w}
	s.handleRuntimeDebug(got, httptest.NewRequest("GET", "/debug/runtime", nil))
	if got.Code != 200 {
		t.Fatalf("status = %d, want 200", got.Code)
	}
	var body runtimeDebugResponse
	if err := json.Unmarshal(got.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Goroutines <= 0 {
		t.Fatalf("goroutines = %d, want a live process count", body.Goroutines)
	}
	if body.Heap.AllocBytes == 0 {
		t.Fatalf("heap/gc snapshot looks empty: %+v", body)
	}
	if body.UptimeSeconds < 4 {
		t.Fatalf("uptime = %f, want at least 4 seconds", body.UptimeSeconds)
	}
}

func TestRuntimeWatchSampleCopiesMemStats(t *testing.T) {
	w := &runtimeWatch{startedAt: time.Now(), read: func(ms *runtime.MemStats) {
		ms.HeapAlloc = 11
		ms.HeapInuse = 22
		ms.HeapSys = 33
		ms.HeapObjects = 44
		ms.NumGC = 2
		ms.PauseTotalNs = 55
		ms.PauseNs[1] = 66
	}}
	s := w.sample()
	if s.HeapAlloc != 11 || s.HeapInuse != 22 || s.HeapSys != 33 || s.HeapObjects != 44 {
		t.Fatalf("heap stats = %+v", s)
	}
	if s.GCCount != 2 || s.GCPauseTotal != 55 || s.GCLastPause != 66 {
		t.Fatalf("gc stats = %+v", s)
	}
}
