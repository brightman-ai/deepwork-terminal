/**
 * versionPanel — 版本面板里那两句话的**措辞与动作**（纯函数，模板对判定一无所知）。
 *
 * ## 为什么是两条，不是一条
 *
 * 「有更新」在这个产品里其实是**两件不同的事**，合并成一句话是错的：
 *
 * | | 是什么 | 用户要做什么 | 代价 |
 * |---|---|---|---|
 * | **页面** | 服务器重新部署过，浏览器还跑着旧 JS chunk | 刷新 | 一次点击，瞬时 |
 * | **程序** | GitHub 上打了新 tag | 去下载重装 | 离开应用，在机器上操作 |
 *
 * 紧迫度、动作、代价三者都不同 —— 所以面板里它们是两块，各自一句话、各自一个动作。
 *
 * ## 四态里最要紧的是 unknown
 *
 * `unknown`（离线 / 还没查到）**绝不能塌成 current**。对着一次失败的查询显示「✓ 已是最新」，
 * 是一句自信的假话，而这枚徽标存在的全部意义就是让人能相信它。服务端因此下发的是四态而不是
 * 「有没有 latest 字段」——见 release_check.go 里 ReleaseState 的注释。
 */

/** 服务端下发的四态（release_check.go 的 ReleaseState，逐字对应）。 */
export type ReleaseState = 'local' | 'current' | 'outdated' | 'unknown'

export type Tone = 'ok' | 'warn' | 'muted'

export interface PanelLine {
  tone: Tone
  text: string
  /** 有动作才给；label 是按钮文案。 */
  action?: { label: string; kind: 'refresh' | 'open'; href?: string }
}

/** 「浏览器页面」这一块。stale 来自 useAppUpdate（比对 asset hash，本地判定，零成本）。 */
export function pageLine(stale: boolean): PanelLine {
  return stale
    ? {
        tone: 'warn',
        text: '服务器已更新，这个页面还是旧的',
        action: { label: '刷新页面', kind: 'refresh' },
      }
    : { tone: 'ok', text: '与服务器一致' }
}

/** 「服务端程序」这一块。 */
export function programLine(
  state: ReleaseState,
  latest?: string,
  latestUrl?: string,
): PanelLine {
  switch (state) {
    case 'outdated':
      // 只有这一档给外链：它是唯一一个"用户需要离开应用去做点什么"的状态。
      return {
        tone: 'warn',
        text: `GitHub 上有新版本 ${latest}`,
        action: { label: '查看发布', kind: 'open', href: latestUrl },
      }
    case 'current':
      return { tone: 'ok', text: '已是最新发布版' }
    case 'local':
      // 对开发者说"有新版本"是噪音甚至误导（他手上的代码可能比 release 还新），
      // 所以这里只解释"为什么这块是空的"，不做任何比较。
      return { tone: 'muted', text: '本地构建，不检查发布版本' }
    case 'unknown':
    default:
      return { tone: 'muted', text: '没查到发布信息（离线？）' }
  }
}

/**
 * 徽标上那枚小圆点该不该亮 —— **只有"需要用户做点什么"才亮**。
 *
 * 这是克制的关键：`current`/`local`/`unknown` 都不亮。一个常亮的点会很快被学会忽略，
 * 那它在真的有事时也就不起作用了。
 */
export function badgeNeedsAttention(pageStale: boolean, state: ReleaseState): boolean {
  return pageStale || state === 'outdated'
}
