// Package terminal — proc_cwd.go: 「这个进程此刻在哪个目录」的唯一实现。
package terminal

import "os"

// processCWD 返回某个进程**此刻**的工作目录；取不到就返回 ""，由调用方自己决定怎么退。
//
// ── 为什么它得单独存在 ──────────────────────────────────────────────────────────────────────
// 会话记着的那个 cwd 是**出生时**的目录（UI 新建的标签一律是 `~`）。人几乎一定会 cd 到别处去，
// 而「上传落在哪」「总览卡片显示我在哪」「去哪找 agent 的 transcript」这几件事问的都是**现在**在
// 哪，不是出生时在哪。答错的后果很具体：粘贴的图片落进 home，而不是你正在干活的项目里。
//
// ── 这里此前是两份实现，而且行为不一样 ──────────────────────────────────────────────────────
// `liveCWD(pid)`（overview.go）和 `liveShellCWD(sess)`（files.go）各写了一遍同一个 readlink，
// 前者不校验结果是不是目录，后者校验。同一个问题两个答案，是迟早的事。现在只有这一个入口。
//
// ── 以及：它在 macOS 上从来没工作过 ────────────────────────────────────────────────────────
// 两份实现读的都是 `/proc/<pid>/cwd`，而 **macOS 根本没有 /proc**。于是 readlink 必然报错、必然
// 返回 ""、必然一路回落到「出生时的目录」—— 没有报错，没有日志，只是每一次上传都落在 home。
// 旧注释里其实写着「"" on any error (non-Linux, gone, …)」：非 Linux 会返回空这件事**当初就知道**，
// 只是没人把它当成 bug。现在按平台分派（见 proc_cwd_{linux,darwin,other}.go）。
func processCWD(pid int) string {
	if pid <= 0 {
		return ""
	}
	dir := readProcessCWD(pid)
	if dir == "" {
		return ""
	}
	// 校验它真的还是个目录 —— 合并两份旧实现时取更严的那个：进程可能刚死、目录可能刚被删，
	// 那时 readlink 仍可能返回一个字符串，而把它当 cwd 用会让上传落进一个不存在的地方。
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return ""
	}
	return dir
}
