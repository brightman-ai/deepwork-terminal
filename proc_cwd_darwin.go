//go:build darwin

package terminal

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// macOS 没有 /proc。取别的进程的 cwd 只有两条路：libproc 的 proc_pidinfo（要 cgo）或者 lsof。
// 这里用 lsof —— 系统自带、不引入 cgo，代价是它要 fork 一个进程（实测 ~33ms）。
//
// **正因为它贵，才必须有缓存**：sessions_overview 的推送循环每秒会问一次每个会话的 cwd，几个标签
// 就是每秒几次 fork。TTL 取 1s：既压住了推送循环的重复提问，又不会让「刚 cd 完就粘贴」这种操作
// 读到超过一秒的旧值。
const procCWDCacheTTL = time.Second

// lsof 绝对路径：PATH 是从宿主继承来的、可能被改过，而这是我们要 exec 的东西。
const lsofPath = "/usr/sbin/lsof"

type procCWDEntry struct {
	dir string
	at  time.Time
}

var procCWDCache = struct {
	sync.Mutex
	m map[int]procCWDEntry
}{m: make(map[int]procCWDEntry)}

func readProcessCWD(pid int) string {
	now := time.Now()
	procCWDCache.Lock()
	if e, ok := procCWDCache.m[pid]; ok && now.Sub(e.at) < procCWDCacheTTL {
		procCWDCache.Unlock()
		return e.dir
	}
	procCWDCache.Unlock()

	dir := lsofCWD(pid)

	procCWDCache.Lock()
	// 失败（进程没了/lsof 不在）同样入缓存：否则一个已经消失的会话会让我们每秒都白 fork 一次。
	procCWDCache.m[pid] = procCWDEntry{dir: dir, at: now}
	// 惰性清理：顺手扔掉过期条目，免得长命服务里这张表跟着历史会话一起涨。条目本来就少
	// （最多等于活着的标签数），所以整表扫一遍比维护一个淘汰队列便宜也简单。
	for k, e := range procCWDCache.m {
		if now.Sub(e.at) >= procCWDCacheTTL {
			delete(procCWDCache.m, k)
		}
	}
	procCWDCache.Unlock()
	return dir
}

// lsof -a -d cwd -p <pid> -Fn 的输出是逐字段的：
//
//	p<pid>
//	fcwd
//	n/Users/me/code/project
//
// 我们要的就是那个 n 行。超时 2s 兜底：lsof 偶尔会卡在某个失去响应的挂载点上，而这条路径
// 坐在每秒一次的推送循环里，卡住它就等于卡住整个总览。
func lsofCWD(pid int) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, lsofPath, "-a", "-d", "cwd", "-p", strconv.Itoa(pid), "-Fn").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "n") && len(line) > 1 {
			return strings.TrimSpace(line[1:])
		}
	}
	return ""
}
