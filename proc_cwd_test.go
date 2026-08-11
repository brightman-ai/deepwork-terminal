package terminal

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 这个测试就是那次事故的反向验证。
//
// 症状：在终端里粘贴的文件落进了 home，而不是你正在干活的目录。根因：取「进程此刻在哪个目录」
// 的实现只读 /proc/<pid>/cwd —— 而 **macOS 根本没有 /proc**，于是它在 mac 上从来没成功过一次，
// 每次都静默回落到「会话出生时的目录」（新建标签一律是 ~）。
//
// 用**本进程自己**当被测对象：它的 cwd 是已知的，所以这条断言在任何平台上都能判真假。
// 只要哪天有人把实现换回一个平台专用的路径，这里就红。
func TestProcessCWD_ResolvesOwnWorkingDir(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skipf("本平台没有取别的进程 cwd 的实现（见 proc_cwd_other.go），跳过：%s", runtime.GOOS)
	}

	want, err := os.Getwd()
	require.NoError(t, err)

	got := processCWD(os.Getpid())
	require.NotEmpty(t, got,
		"取不到自己的 cwd —— 在 %s 上这个探测是坏的，一切依赖它的地方都会悄悄回落到会话启动目录", runtime.GOOS)

	// 比较前各自解一次符号链接：macOS 的 /tmp 是 /private/tmp 的软链，lsof 报的是解析后的真实
	// 路径而 Getwd 可能报软链路径。两边都解开，比的才是「是不是同一个目录」而不是「字符串是否相等」。
	wantReal, err := filepath.EvalSymlinks(want)
	require.NoError(t, err)
	gotReal, err := filepath.EvalSymlinks(got)
	require.NoError(t, err)
	assert.Equal(t, wantReal, gotReal)
}

func TestProcessCWD_RejectsInvalidPID(t *testing.T) {
	assert.Empty(t, processCWD(0), "pid 0 不是一个进程")
	assert.Empty(t, processCWD(-1))
	// 一个几乎确定不存在的 pid：取不到就该老实返回 ""，让调用方走自己的兜底，
	// 而不是抛出或者编一个目录出来。
	assert.Empty(t, processCWD(1<<30))
}

// darwin 侧的 lsof 调用有 TTL 缓存（sessions_overview 每秒会问一次，每次 fork 一个 lsof 太贵）。
// 缓存最容易出的错是「第二次拿到的东西不一样」，所以这里连问两次必须同解。
func TestProcessCWD_StableAcrossCalls(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("同上")
	}
	first := processCWD(os.Getpid())
	require.NotEmpty(t, first)
	assert.Equal(t, first, processCWD(os.Getpid()), "同一个 pid 连问两次必须同解（缓存不能改写答案）")
}
