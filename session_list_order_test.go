package terminal

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

// List() 的顺序是承重的，不是好看：pro 的整条标签栏直接由它派生，标签上的数字又是
// `前缀+N` 直达与总览卡片编号的依据（"你看到的数字就是你按的数字"）。
// sync.Map.Range 的遍历顺序未定义且每次都可能不同 —— 曾导致新建的终端落到第一位、
// 显示成"终端1"（同时另一个更老的标签也显示"终端1"），且每次轮询都重新洗牌。
// 管道假 PTY：这两条测试只关心 List() 的顺序，不需要真进程。
func orderTestFactory(_ PTYStartOptions) (*os.File, *exec.Cmd, error) {
	r, _, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	return r, nil, nil
}

func TestListReturnsStableCreationOrder(t *testing.T) {
	m := NewSessionManagerWithFactory(1024, "/bin/sh", orderTestFactory)

	var ids []string
	for i := 0; i < 8; i++ {
		sess, err := m.CreateWithOptions(CreateOptions{Name: "t", CWD: t.TempDir()})
		if err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
		// 拉开创建时间，避免同一时钟刻度让断言退化成"只验了 ID tie-break"。
		sess.CreatedAt = time.Now().Add(time.Duration(i) * time.Millisecond)
		ids = append(ids, sess.ID)
	}
	t.Cleanup(m.DestroyAll)

	// 连续多次调用必须给出同一个顺序，且就是创建顺序。一次撞对是巧合，反复撞对才是保证。
	for round := 0; round < 5; round++ {
		got := m.List()
		if len(got) != len(ids) {
			t.Fatalf("round %d: got %d sessions, want %d", round, len(got), len(ids))
		}
		for i, sess := range got {
			if sess.ID != ids[i] {
				t.Fatalf("round %d: position %d is %s, want %s (创建顺序被打乱 → 标签编号会漂)",
					round, i, sess.ID, ids[i])
			}
		}
	}
}

// 同一时钟刻度创建的两个会话也必须有稳定的先后，否则顺序仍会在两者之间闪。
func TestListBreaksCreatedAtTiesDeterministically(t *testing.T) {
	m := NewSessionManagerWithFactory(1024, "/bin/sh", orderTestFactory)
	same := time.Now()
	for i := 0; i < 6; i++ {
		sess, err := m.CreateWithOptions(CreateOptions{Name: "t", CWD: t.TempDir()})
		if err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
		sess.CreatedAt = same
	}
	t.Cleanup(m.DestroyAll)

	first := m.List()
	for round := 0; round < 5; round++ {
		got := m.List()
		for i := range got {
			if got[i].ID != first[i].ID {
				t.Fatalf("round %d: 同刻度创建的会话顺序不稳定 (位置 %d: %s vs %s)",
					round, i, got[i].ID, first[i].ID)
			}
		}
	}
}
