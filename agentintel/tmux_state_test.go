package agentintel

import (
	"bytes"
	"context"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/brightman-ai/kit/obs"
)

// A probe that ran out of time did not observe "no panes" — it observed nothing. Publishing
// the two as the same empty topology is what made the pane bar blink out and back: the empty
// answer was cached for a full TTL, then a good probe restored it, then another timed out.
//
// The guard for this was already written and already commented; it just watched the PARENT
// context, while the timeout that fires lives on the per-command child. The parent stays
// healthy, so it never triggered once in the case it was written for.
func TestTopologySnapshotKeepsLastGoodWhenTheProbeRunsOutOfTime(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	s := NewTmuxStateService()
	if !s.TmuxInstalled() {
		t.Skip("no tmux")
	}
	known := TmuxState{
		Installed: true, ServerRunning: true,
		Sessions: []TmuxSessionState{{Name: "known", Windows: []TmuxWindowState{{Index: 1}}}},
	}
	s.topologyMu.Lock()
	s.topology, s.topologyRead, s.topologyAt = known, true, time.Now().Add(-time.Hour)
	s.topologyMu.Unlock()

	// A budget nothing can meet: every command's context is already expired when it starts,
	// while the caller's own context is perfectly healthy — the exact shape of the bug.
	s.cmdTimeout = time.Nanosecond

	got := s.topologySnapshot(context.Background())
	if len(got.Sessions) != 1 || got.Sessions[0].Name != "known" {
		t.Fatalf("a probe that could not answer replaced what we knew: %+v", got.Sessions)
	}
	s.topologyMu.Lock()
	cached := s.topology
	s.topologyMu.Unlock()
	if len(cached.Sessions) != 1 || cached.Sessions[0].Name != "known" {
		t.Fatalf("the unanswered probe was cached, pinning it for a whole TTL: %+v", cached.Sessions)
	}
}

// The converse, so the fix cannot become "never update": a probe that DOES answer replaces the
// cache, including when its honest answer is that the tmux server is gone.
func TestTopologySnapshotPublishesAnAnswerEvenWhenItIsEmpty(t *testing.T) {
	s := NewTmuxStateService()
	s.mu.Lock()
	s.installed, s.installedAt = false, time.Now() // "tmux is not installed" IS an answer
	s.mu.Unlock()
	s.topologyMu.Lock()
	s.topology = TmuxState{Sessions: []TmuxSessionState{{Name: "stale"}}}
	s.topologyRead, s.topologyAt = true, time.Now().Add(-time.Hour)
	s.topologyMu.Unlock()

	if got := s.topologySnapshot(context.Background()); len(got.Sessions) != 0 {
		t.Fatalf("an answered probe did not replace a stale topology: %+v", got.Sessions)
	}
}

// ── ServerVanished：一个我们看着的 server 死了，和「你从不用 tmux」不是同一件事 ────────────────
//
// 2026-08-08 19:34:22，一台跑了十一天的 tmux server 吃了 SIGSEGV。pane bar 的门是
// `attached && windows.length`，于是它直接消失——和「这个 shell 本来就不在 tmux 里」长得一模一样。
// 全程序唯一的痕迹是一行 INFO「window-size unreadable」，使用者是自己敲 `tmux attach` 看到
// "no sessions" 才知道的。那是「观察不到 ≠ 不存在」被反过来用了一次：我们**确实观察到了消失**，
// 却把它发布成了什么都没发生。
//
// 三条独立会各自失效，所以各测一条。

func vanishService(t *testing.T) *TmuxStateService {
	t.Helper()
	s := NewTmuxStateService()
	// tmux「没装」是一个 ANSWER，所以探测会走完并落到判定上，同时一条 tmux 命令都不会发出去
	// ——这个用例不许碰机器上任何真实的 tmux。
	s.mu.Lock()
	s.installed, s.installedAt = false, time.Now()
	s.mu.Unlock()
	return s
}

func TestServerVanished_StaysSilentForSomeoneWhoNeverRanTmux(t *testing.T) {
	s := vanishService(t)
	got := s.topologySnapshot(context.Background())
	if got.ServerVanished {
		t.Fatal("一个从来没开过 tmux 的人被告知他的 tmux 死了 —— 这条提示只要误报一次就再也没人信它")
	}
}

func TestServerVanished_FiresOnceWhenAServerWeWatchedDisappears(t *testing.T) {
	s := vanishService(t)
	// 我们确实看见过它：一个带会话的 server。
	s.topologyMu.Lock()
	s.sawSessions = true
	s.topology = TmuxState{Sessions: []TmuxSessionState{{
		Name: "gone", Windows: []TmuxWindowState{{Index: 1}, {Index: 2}},
	}}}
	s.topologyRead, s.topologyAt = true, time.Now().Add(-time.Hour)
	s.topologyMu.Unlock()

	before := readCounter(t, "tmux_server_vanished_total")
	got := s.topologySnapshot(context.Background())
	if !got.ServerVanished {
		t.Fatal("我们看着的 server 没了，payload 却只字未提 —— UI 里 pane bar 的消失就再次无从区分")
	}
	if delta := readCounter(t, "tmux_server_vanished_total") - before; delta != 1 {
		t.Fatalf("消失事件计了 %d 次，要恰好 1 次", delta)
	}

	// 还在消失状态里的每一次 tick 都要照说不误（重连/刷新的客户端才学得到），
	// 但**只报一次**，否则一台没装 tmux 的机器每秒一条 WARN。
	s.topologyMu.Lock()
	s.topologyAt = time.Now().Add(-time.Hour)
	s.topologyMu.Unlock()
	again := s.topologySnapshot(context.Background())
	if !again.ServerVanished {
		t.Fatal("消失是个持续状态，不是一次性事件：下一个连上来的客户端也得知道")
	}
	if delta := readCounter(t, "tmux_server_vanished_total") - before; delta != 1 {
		t.Fatalf("同一次消失被计了 %d 次", delta)
	}
}

func TestServerVanished_ANonAnsweringProbeIsNotADisappearance(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	s := NewTmuxStateService()
	if !s.TmuxInstalled() {
		t.Skip("no tmux")
	}
	s.topologyMu.Lock()
	s.sawSessions = true
	s.topology = TmuxState{Installed: true, ServerRunning: true,
		Sessions: []TmuxSessionState{{Name: "known", Windows: []TmuxWindowState{{Index: 1}}}}}
	s.topologyRead, s.topologyAt = true, time.Now().Add(-time.Hour)
	s.topologyMu.Unlock()
	// 谁也满足不了的预算：这是「问不出来」，不是「它不在了」。把两者混为一谈，就等于把
	// 上面那条 TestTopologySnapshot… 修掉的 bug 换个出口重新放出来。
	s.cmdTimeout = time.Nanosecond

	if got := s.topologySnapshot(context.Background()); got.ServerVanished {
		t.Fatal("一次没能答上来的探测被当成了 server 消失 —— 慢机器会被告知它的 tmux 死了")
	}
}

// readCounter reads a counter out of the metric registry. There is no accessor — WritePrometheus
// is the only reader — so this parses the exposition text, which is also what a scrape sees.
func readCounter(t *testing.T, name string) int64 {
	t.Helper()
	var buf bytes.Buffer
	obs.WritePrometheus(&buf)
	prefix := name + " "
	for _, line := range strings.Split(buf.String(), "\n") {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(line, prefix)), 10, 64)
		if err != nil {
			t.Fatalf("unparsable %s line %q: %v", name, line, err)
		}
		return n
	}
	t.Fatalf("%s is not registered — the disappearance has no metric at all", name)
	return 0
}
