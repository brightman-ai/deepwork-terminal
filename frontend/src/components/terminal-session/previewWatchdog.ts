/**
 * previewWatchdog — 预览渲染的"失败必须可见"兜底（2026-09-11，REQ-fp-a2-0）。
 *
 * 背景：移动端 Safari 出现过 pdf/docx 点开"黑屏没内容"（根因未定，见 run
 * 20260911-110817-files-panel-term）。两个渲染器（pdfjs / docx-preview）对**抛错**都有
 * try/catch 报错文案，但对**永不结算**（worker 挂死、canvas 静默失败、字体/布局卡死）没有
 * 任何出口——promise 永远 pending，用户就永远看着一块黑。静默黑屏本身就是产品缺陷
 * （GUARDRAILS §7：环境触发不豁免产品响应层），而且它把诊断信号也吞了。
 *
 * 规则：渲染器开工时 arm()，**首帧真像素**落地时 settle()；超时未 settle → 回调给出人话
 * 文案（含"下载"逃生口指路）。这个 watchdog 不是根因修复，是把失败变成可读信号——
 * 用户报"看到什么文案"就能把假设矩阵收敛成单选。
 */

export interface RenderWatchdog {
  /** 渲染成功（首帧内容真实落进 DOM）时调用；若已超时则无事发生。 */
  settle(): void
  /** 组件卸载/重新加载时调用，解除定时器且不再触发。 */
  dispose(): void
}

export function armRenderWatchdog(
  what: string,
  onTimeout: (message: string) => void,
  ms = 12_000,
): RenderWatchdog {
  let finished = false
  const timer = setTimeout(() => {
    if (finished) return
    finished = true
    onTimeout(
      `${what}长时间没有渲染出来（超过 ${Math.round(ms / 1000)} 秒）。` +
        '可能是浏览器插件、内存不足或渲染器与当前浏览器不兼容——可用上方「下载」在本地应用中打开。',
    )
  }, ms)
  return {
    settle() {
      if (finished) return
      finished = true
      clearTimeout(timer)
    },
    dispose() {
      finished = true
      clearTimeout(timer)
    },
  }
}
