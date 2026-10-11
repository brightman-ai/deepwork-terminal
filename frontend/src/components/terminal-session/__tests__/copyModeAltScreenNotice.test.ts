import { describe, expect, test } from 'bun:test'
import { readFileSync } from 'node:fs'

const surface = readFileSync(new URL('../CliTerminalSurface.vue', import.meta.url), 'utf8')
const view = readFileSync(new URL('../CopyModeView.vue', import.meta.url), 'utf8')

// 组件挂载环境本仓库没有，所以接线用源码守卫锁住（同 copyModeEntry.test.ts 的做法）。
// 这条接线断了的后果是静默的：回看照常打开，只是少了那句"为什么这里没有它的输出"。
describe('回看：备用屏幕提醒与失败原因', () => {
  test('打开时判断一次"非 tmux 且 xterm 在备用屏幕"，并传给视图', () => {
    const open = surface.indexOf('function openCopyMode()')
    const set = surface.indexOf('copyModeAltScreen.value =', open)
    const opened = surface.indexOf('copyModeOpen.value = true', open)
    expect(open).toBeGreaterThan(-1)
    expect(set).toBeGreaterThan(open)
    // 必须在视图挂载（copyModeOpen=true）之前定下来，否则视图第一帧拿到的是上一次的值。
    expect(set).toBeLessThan(opened)
    const decision = surface.slice(set, opened)
    expect(decision).toContain('!tmuxAttached.value')
    expect(decision).toContain("buffer.active.type === 'alternate'")
    expect(surface).toContain(':alt-screen="copyModeAltScreen"')
  })

  test('视图渲染提醒条，且空状态不再硬编码"还没有历史"而是带上请求错误', () => {
    expect(view).toContain('altScreenNotice(props.altScreen)')
    expect(view).toContain('data-testid="copy-mode-alt-notice"')
    expect(view).toContain('emptyHistoryText(h.error.value)')
    expect(view).not.toMatch(/class="copy-mode__empty"[^>]*>\s*这个终端还没有滚出去的历史。/)
  })
})
