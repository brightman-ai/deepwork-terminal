/**
 * useTabDisplayName — D4: tabs show as "终端1"/"终端2" by DISPLAY POSITION (left to right),
 * renumbering automatically when a tab in the middle closes. A tab the user has explicitly
 * renamed keeps its custom name forever — only an auto-generated "终端 N" style name is treated
 * as a live position placeholder and recomputed on every render.
 *
 * This is a heuristic (regex on the stored name) rather than a schema flag on WorkbenchTab,
 * matching nextTabName()'s existing "终端 N" generation without a migration: any name that
 * still LOOKS auto-generated is safe to keep overriding; the moment a user renames a tab to
 * literally anything else, it stops matching and freezes.
 */

// Matches BOTH shells' auto-generated names, so one rule numbers both:
//   standalone (useCliState.nextTabName) → "终端 1", "终端 2"
//   pro        (useCliV2.createSession)  → bare "终端" (server stores name='终端' verbatim)
// A user-chosen name ("部署脚本", "终端 - 备份") never matches and is left frozen.
const DEFAULT_NAME_RE = /^终端\s*\d*$/

export function isDefaultTabName(name: string): boolean {
  return DEFAULT_NAME_RE.test(name.trim())
}

/** The label to render for a tab: live "终端{position}" for an untouched default name, else the user's own name. */
export function displayTabName(name: string, position: number | undefined): string {
  if (position !== undefined && isDefaultTabName(name)) return `终端${position}`
  return name
}

/**
 * 同一个标签，但**名字里不带编号** —— 给那些自己另外显示位置角标的地方用（标签栏）。
 *
 * 编号只该有一个落点。让默认名渲染成「终端3」、而改过名的「build」什么编号都没有，等于把同一个
 * 信息放在两个不同的位置上：眼睛扫过去要在名字里找一次、在角标上再找一次，而且两处还不总是都有。
 * 标签栏因此改成「名字一律不带编号 + 角标一律带」——所有标签一视同仁，编号永远在同一个地方。
 *
 * 其余没有角标的地方（总览卡片、右键菜单、底栏提示）继续用 displayTabName：那里名字是编号的
 * **唯一**载体，摘掉就真的找不回来了。
 */
export function tabBaseName(name: string): string {
  const trimmed = name.trim()
  return isDefaultTabName(trimmed) ? '终端' : name
}
