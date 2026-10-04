import { describe, expect, test } from 'bun:test'
import { readFileSync } from 'node:fs'

const surface = readFileSync(new URL('../CliTerminalSurface.vue', import.meta.url), 'utf8')
const toolbar = readFileSync(new URL('../Toolbar.vue', import.meta.url), 'utf8')
const button = readFileSync(new URL('../CopyModeButton.vue', import.meta.url), 'utf8')
const tmuxBar = readFileSync(new URL('../TmuxQuickBar.vue', import.meta.url), 'utf8')

describe('应用 Copy Mode 显式入口', () => {
  test('桌面入口位于 RTT chip 后、输入条入口前，并调用现有回看视口', () => {
    const chip = surface.indexOf('<ConnectionChip')
    const appButton = surface.indexOf('<CopyModeButton', chip)
    const compose = surface.indexOf('data-testid="surface-compose-toggle"', appButton)

    expect(chip).toBeGreaterThan(-1)
    expect(appButton).toBeGreaterThan(chip)
    expect(compose).toBeGreaterThan(appButton)
    expect(surface.slice(appButton, compose)).toContain('placement="status"')
    expect(surface.slice(appButton, compose)).toContain('@open="openCopyMode"')
    expect(surface).toContain(':disabled="props.isRemote"')
  })

  test('移动端入口紧邻 compose toggle，走同一个 openCopyMode handler', () => {
    const entry = toolbar.indexOf('placement="toolbar"')
    const compose = toolbar.indexOf("$emit('toggleCompose')", entry)
    expect(entry).toBeGreaterThan(-1)
    expect(compose).toBeGreaterThan(entry)
    expect(toolbar.slice(entry, compose)).toContain("$emit('openCopyMode')")
    expect(surface).toContain('@open-copy-mode="openCopyMode"')
    expect(surface).toContain(':copy-mode-disabled="props.isRemote"')
  })

  test('按钮图示可识别为历史回看，禁用原因可见，手机 hit target 至少 44px', () => {
    expect(button).toContain("import { History } from 'lucide-vue-next'")
    expect(button).toContain('<span>回看</span>')
    expect(button).toContain('回看历史（应用 Copy Mode）')
    expect(button).toContain('远程终端暂不支持回看历史')
    expect(button).toContain('min-width: 44px')
    expect(button).toContain('min-height: 44px')
  })

  test('tmux quick bar 的 cp 仍是 tmux 原生入口，应用视口是独立入口', () => {
    expect(tmuxBar).toContain('data-testid="tmux-quick-cp"')
    expect(tmuxBar).toContain("tmux.prefixSeq('[')")
    expect(surface).toContain('<CopyModeView')
    expect(surface).toContain('v-if="copyModeOpen"')
  })
})
