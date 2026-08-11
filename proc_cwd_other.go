//go:build !linux && !darwin

package terminal

// 其它平台（Windows / BSD …）暂时没有取别的进程 cwd 的实现，返回 "" 让调用方走它们各自的兜底
// （会话启动目录）。这不是「静默降级」：它是这个平台上**已知**没有的能力，而且是这一族文件里
// 唯一一个显式说出「我不知道」的地方 —— 而不是像从前那样，在一个 Linux 专用的 readlink 上
// 撞出 error 再假装什么都没发生。
func readProcessCWD(int) string { return "" }
