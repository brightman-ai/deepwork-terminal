//go:build linux

package terminal

import (
	"fmt"
	"os"
)

// Linux: /proc/<pid>/cwd 是一个指向真实目录的符号链接，readlink 是一次内存里的操作 ——
// 微秒级，没有 fork，所以不需要缓存（sessions_overview 每秒调它一次也无所谓）。
func readProcessCWD(pid int) string {
	dir, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid))
	if err != nil {
		return ""
	}
	return dir
}
