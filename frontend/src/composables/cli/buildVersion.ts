/**
 * buildVersion — 顶栏那枚版本徽标的**显示格式**。
 *
 * 分工：**服务端说全，UI 决定显示多长**。`GET /version` 返回的是完整、诚实的身份
 * （`terminal.BuildVersion`，见 version.go）——`--version` 和 bug 报告需要那份细节。这里只负责
 * 把它压进顶栏最挤的那一格，完整串永远留在徽标的 title 里，一 hover 就能拿到。
 *
 * 为什么要压：这枚徽标待的地方是顶栏最右一簇，旁边挨着 4 枚图标。`dev-b2535a0-dirty` 17 个字符，
 * 把那一行撑得没法看（Human 实测原话："版本号改短一点吧,太长了"）。
 *
 * | 构建方式 | 服务端返回 | 徽标显示 |
 * |---|---|---|
 * | 发版（goreleaser 打 tag） | `v0.7.14` | `v0.7.14` |
 * | build.sh（git describe，正好在 tag 上） | `v0.7.14` | `v0.7.14` |
 * | 裸 `go build`（VCS 戳） | `dev-b2535a0-dirty` | `b2535a0*` |
 * | tag 之后又走了几个提交 | `v0.2.0-3-gabc1234` | `v0.2.0+` |
 *
 * **为什么不带分支名**（Human 提过 `main_xxx` 这种形态）：分支几乎永远是 `main`，它会是这串里
 * 最长、信息量最低的一段，而这里恰恰是整个顶栏最缺地方的一格。真正回答"我跑的是不是我刚构建的
 * 那份"的是那 7 位 hash；有 tag 的时候 tag 更适合给人读，所以 tag 优先。
 *
 * `*` 而不是 `-dirty`：省 5 个字符，而且 `*` 在 git/编辑器里本来就是"有未提交改动"的通用记号。
 */

/** 完整身份（给 title 用）。数字开头的补上 `v`，其余原样——服务端说什么就是什么。 */
export function formatFullVersion(raw: string): string {
  const v = raw.trim()
  if (!v) return ''
  return /^\d/.test(v) ? 'v' + v : v
}

/**
 * 徽标显示的短形式。空串表示"没有版本可显示" → 调用方应把徽标整个藏起来，而不是显示一个空框。
 */
export function formatShortVersion(raw: string): string {
  const full = formatFullVersion(raw)
  if (!full) return ''
  // ① 带语义化版本号的（发版 / git describe）→ 只留 vX.Y.Z。
  //    但**不能把"不在 tag 上"这件事一起丢掉**：`git describe` 的 `-3-g<hash>` / `-dirty`
  //    意味着这个构建**领先于或偏离了那个 tag**，而这恰恰是本地部署最常见的状态（pro 天天把
  //    WIP 推到 8087）。丢了它，一个 WIP 构建会显示得和正式发版一模一样。用一个 `+` 表示
  //    "在这个 tag 之后还有东西"——一个字符，含义明确，不占地方。
  const semver = full.match(/^v?(\d+\.\d+\.\d+)(.*)$/)
  if (semver) return `v${semver[1]}${semver[2] ? '+' : ''}`
  // ② 未打标的构建：`dev-<hash>` / `dev-<hash>-dirty` → `<hash>` / `<hash>*`。
  //    `dev-` 前缀不带信息（没 tag 本来就说明它不是发版），删掉；hash 是唯一有身份的那一段。
  const dev = full.match(/^dev-([0-9a-f]{7,40})(-dirty)?$/i)
  if (dev) return dev[1].slice(0, 7) + (dev[2] ? '*' : '')
  // ③ 兜底：认不出的形态原样显示。宁可长一点，也不要瞎猜着截断出一个错的身份。
  return full
}
