package muxd

import "strings"

// agentSessionMarkers —— 必须**摘掉**、不能传给 PTY 子进程的环境变量。
//
// ── 这是在修一个真实事故，不是防御性洁癖 ────────────────────────────────────────────────────
// 终端宿主（dw-host）如果**自己**是从某个 agent CLI 的 session 里被拉起来的（比如有人在 Claude
// Code 里敲了一句 `run_cli.sh`），那一刻它的进程环境就永久沾上了那个 session 的身份标记。宿主是
// 常驻的、几个月不重启，于是它之后开出的**每一个**终端面板 → 每一个 shell → 你在面板里敲的每一个
// `claude`，都顺着进程树继承了这份标记。
//
// 后果是静默的：Claude Code 用 `CLAUDE_CODE_CHILD_SESSION` 识别「我是被另一个 Claude Code 当子进程
// 拉起来的嵌套 session」，嵌套 session 默认**不落盘 transcript**（避免和父 session 抢同一个文件）。
// 于是使用者在 webui 里开的每一个 claude 都不写 transcript —— 没有报错，没有提示，只是记录不见了。
// 实测（2026-08-10 那台常驻 dw-host，8/10 19:57 启动后再没重启）：进程环境里坐着
// `CLAUDE_CODE_CHILD_SESSION=1` + `CLAUDE_CODE_SESSION_ID=…`，四层进程全带着。
//
// ── 为什么住在 muxd 里 ───────────────────────────────────────────────────────────────────
// 因为 PTY 现在由 daemon 生成。泄漏链只有一条：拉起 daemon 的那个进程 → PTY → 里面跑的 agent，
// 而 daemon 恰恰比宿主更长寿（宿主重启它都不重启），所以**它继承来的脏环境活得更久、传得更广**。
// 洗在这里是库级的：任何宿主、任何部署形态都自动生效，不需要每个宿主各自记得洗一遍；而且只洗
// 「交给子进程的那一份」，daemon 自己的环境原样留着 —— 那份脏环境恰恰是当初查出根因的凭据。
//
// ── 名单为什么是逐条列的，而不是按 `CLAUDE_CODE_*` 前缀一刀切 ─────────────────────────────────
// 同一个前缀下**身份**和**配置**混在一起，一刀切会把使用者真正想要的设置也摘掉：
//
//	摘：身份/血缘标记（我是谁的子进程、我的 session id、我在什么沙箱里）——继承下来一定是错的。
//	留：配置（CLAUDE_CODE_MAX_OUTPUT_TOKENS、CODEX_HOME…）与凭据（CODEX_API_KEY、ACCESS_TOKEN…）
//	    ——继承下来正是使用者要的，摘了会让面板里的 agent 变得没配置、甚至登不上。
//
// 新增条目前先问一句：这个变量回答的是「我是谁的孩子」还是「我该怎么工作」。前者才进这张表。
var agentSessionMarkers = []string{
	// Claude Code —— 元凶就在这一组（本机 + 事故机双向实证）。
	"CLAUDECODE",                // "你正跑在 Claude Code 里"
	"CLAUDE_CODE_CHILD_SESSION", // 嵌套标记：直接关掉 transcript 落盘
	"CLAUDE_CODE_SESSION_ID",
	"CLAUDE_CODE_ENTRYPOINT",
	"CLAUDE_CODE_EXECPATH",
	"CLAUDE_PID",         // 父 session 的进程号
	"CLAUDE_PLUGIN_DATA", // 父 session 的插件态
	// Codex —— 变量名取自 codex 主二进制里的字符串表 + 本机实测，不是猜的。
	"CODEX_THREAD_ID",            // 线程/会话身份
	"CODEX_COMPANION_SESSION_ID", // codex companion 的 session 身份
}

// 下面这几个**刻意不摘**，尽管它们的名字看起来同族 —— 记在这里，免得下一个人"顺手补全"：
//
//   - `CLAUDE_EFFORT`：这是**配置**（推理档位），不是血缘。摘掉它等于替使用者悄悄改设置。
//     判据就是上面那句：它回答的是「我该怎么工作」，不是「我是谁的孩子」。
//   - `CODEX_SANDBOX` / `CODEX_SANDBOX_NETWORK_DISABLED`：这是**执行边界的事实**，不是身份。
//     如果 PTY 其实仍在同一个 OS 沙箱里（我们无从判断），摘掉它只会让子 agent 对自己有没有网络
//     做出错误判断 —— 那比继承更危险。「是否跨出了沙箱」只有真正拉起这个进程的人知道，不能靠
//     一个变量名前缀去推断。
//
// 判据一旦写下就得自己守住：第一版把这三个也摘了，正好违反了上面那段注释。

// PTYEnv returns the environment to hand a PTY child: the caller's environment minus
// the agent-identity markers above, with TERM/COLORTERM normalised.
func PTYEnv(env []string) []string {
	drop := make(map[string]struct{}, len(agentSessionMarkers))
	for _, k := range agentSessionMarkers {
		drop[k] = struct{}{}
	}
	out := make([]string, 0, len(env)+2)
	for _, item := range env {
		if strings.HasPrefix(item, "TERM=") || strings.HasPrefix(item, "COLORTERM=") {
			continue
		}
		// 按**键**精确比对，不是按前缀 —— 见 agentSessionMarkers 的注释。没有 '=' 的畸形条目
		// 原样放行（那不是我们要管的事）。
		if i := strings.IndexByte(item, '='); i > 0 {
			if _, bad := drop[item[:i]]; bad {
				continue
			}
		}
		out = append(out, item)
	}
	out = append(out, "TERM=xterm-256color", "COLORTERM=truecolor")
	return out
}

// SplitShell tokenizes a shell command string into program + args, honouring single and
// double quotes and backslash escapes (POSIX shell-words style). A single bare token
// (e.g. "/bin/zsh") yields that token and no args. An empty/whitespace-only string
// yields an empty program, which exec rejects with a clear error.
//
// It has to handle args because a session's command is configurable and routinely
// carries them ("/bin/bash --login", "tmux attach -t x").
func SplitShell(s string) (prog string, args []string) {
	var (
		tokens  []string
		cur     strings.Builder
		inToken bool
		quote   rune // 0, '\'' or '"'
	)
	flush := func() {
		if inToken {
			tokens = append(tokens, cur.String())
			cur.Reset()
			inToken = false
		}
	}
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch {
		case quote == '\'':
			if c == '\'' {
				quote = 0
			} else {
				cur.WriteRune(c)
			}
		case quote == '"':
			if c == '"' {
				quote = 0
			} else if c == '\\' && i+1 < len(rs) && (rs[i+1] == '"' || rs[i+1] == '\\') {
				i++
				cur.WriteRune(rs[i])
			} else {
				cur.WriteRune(c)
			}
		case c == '\'' || c == '"':
			quote = c
			inToken = true
		case c == '\\' && i+1 < len(rs):
			i++
			cur.WriteRune(rs[i])
			inToken = true
		case c == ' ' || c == '\t' || c == '\n':
			flush()
		default:
			cur.WriteRune(c)
			inToken = true
		}
	}
	flush()
	if len(tokens) == 0 {
		return "", nil
	}
	return tokens[0], tokens[1:]
}
