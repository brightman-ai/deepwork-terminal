import { describe, expect, test } from 'bun:test'
import { readFileSync } from 'node:fs'

// REQ-TI-06 的结构性守卫：输入框浮在终端之上、不占布局，所以出现 / 收起 / 拖动都不会让 .terminal-body
// 变高变矮，也就不会触发 xterm 的 ResizeObserver → 不会向服务端发 resize 帧。真正的"没有 resize 帧"
// 要在浏览器里量；这里钉住的是它成立的**结构前提**——一旦有人把它改回 flex 行或挪出 .terminal-body，
// 这条先红。（源码文本断言，沿用 copyModeEntry.test.ts 的做法。）
const surface = readFileSync(new URL('../CliTerminalSurface.vue', import.meta.url), 'utf8')
const bar = readFileSync(new URL('../ComposeBarDesktop.vue', import.meta.url), 'utf8')
const xterm = readFileSync(new URL('../XtermTerminal.vue', import.meta.url), 'utf8')

/** 同一个选择器可能分几段声明（变量一段、布局一段）：把它所有的块拼起来。 */
function ruleBody(css: string, selector: string): string {
  const blocks: string[] = []
  for (let at = css.indexOf(`\n${selector} {`); at !== -1; at = css.indexOf(`\n${selector} {`, at + 1)) {
    blocks.push(css.slice(at, css.indexOf('}', at)))
  }
  expect(blocks.length).toBeGreaterThan(0)
  return blocks.join('\n')
}

describe('REQ-TI-06 悬浮输入框不改变终端行列数', () => {
  const style = bar.slice(bar.indexOf('<style scoped>'))

  test('根元素是 absolute 浮层，不是 flex 流里的一行', () => {
    const rule = ruleBody(style, '.compose-bar-desktop')
    expect(rule).toContain('position: absolute')
    expect(rule).not.toContain('flex-shrink')
  })

  test('挂在 .terminal-body 里面（它是 position: relative 的定位参照），不是它的兄弟', () => {
    const bodyStart = surface.indexOf('class="terminal-body"')
    const bodyEnd = surface.indexOf('<!-- 底栏 (mobile only) -->')
    const mount = surface.indexOf('<ComposeBarDesktop')
    expect(bodyStart).toBeGreaterThan(-1)
    expect(mount).toBeGreaterThan(bodyStart)
    expect(mount).toBeLessThan(bodyEnd)
    expect(ruleBody(surface.slice(surface.indexOf('<style scoped>')), '.terminal-body')).toContain('position: relative')
  })

  test('没有任何 watch 在输入框开关上触发 fit / resize', () => {
    expect(surface).not.toMatch(/watch\(\s*desktopComposeOpen/)
    expect(surface).not.toMatch(/watch\(\s*\[[^\]]*desktopComposeOpen/)
  })

  test('悬浮在总览(15)、回看(30)、搜索条(60)之下，在 xterm 之上', () => {
    const z = Number(/z-index:\s*(\d+)/.exec(ruleBody(style, '.compose-bar-desktop'))![1])
    expect(z).toBeGreaterThan(10) // "正在恢复"提示
    expect(z).toBeLessThan(15)
  })
})

describe('REQ-TI-01 接线', () => {
  test('keydown 经 xterm 自己的回调拦（排在所有 document 捕获快捷键之后），不另挂 document keydown', () => {
    expect(surface).toContain(':key-intercept="onTerminalKeyIntercept"')
    expect(xterm).toContain('attachCustomKeyEventHandler')
    expect(xterm).toContain("ev.type === 'keydown'")
    expect(surface).not.toMatch(/addEventListener\('keydown', onTypeCompose/)
  })

  test('移动端整条链路不进入', () => {
    const fn = surface.slice(surface.indexOf('function onTerminalKeyIntercept'), surface.indexOf('function onTypeComposeCompositionStart'))
    expect(fn).toContain('!isMobile.value')
  })

  test('所有直达终端的用户输入都过 PromptTracker 记账', () => {
    const fn = surface.slice(surface.indexOf('function sendTerminalData'), surface.indexOf('// The ResizeObserver\'s own path'))
    expect(fn.match(/promptTracker\.noteSent/g)?.length).toBe(2) // 单字节分支 + 多字节分支
  })
})

describe('快速打开输入条的 caret / IME 时序', () => {
  test('挂载后的 focusInput 不再跨 nextTick 延迟，并把首次 focus 的 caret 放到草稿末尾', () => {
    const exposed = bar.slice(bar.indexOf('defineExpose({'), bar.indexOf('\n})', bar.indexOf('defineExpose({')))
    expect(exposed).toContain('focusInput: () =>')
    expect(exposed).not.toContain('await nextTick()')
    expect(exposed).toContain('document.activeElement === ta')
    expect(exposed).toContain('ta.setSelectionRange(end, end)')
  })
})
