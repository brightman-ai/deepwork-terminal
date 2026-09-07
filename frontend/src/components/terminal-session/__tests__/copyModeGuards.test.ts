import { describe, it, expect } from 'bun:test'
import { readFileSync } from 'node:fs'

/**
 * 复制模式的三条硬约束 —— 每一条坏掉都**不会报错**，只会安静地变成另一种更糟的行为：
 *
 *   ① 进了模式，一个按键都不许漏进 PTY。漏一个字符进 shell，比这个功能不存在更糟。
 *   ② 文本必须能用**原生方式**选中复制。这是它相对 xterm canvas 的全部优势，没了就白做。
 *   ③ attach 了 tmux 的标签不开这个模式。那时 `Ctrl+B` 整个归 tmux，再叠一层只会打架。
 *
 * 这里用**源码结构断言**（grep 级）来守，而不是行为断言：三条都埋在 SFC 里，这个项目没有组件级
 * DOM 测试设施，而"没有断言"和"断言写不出来"在回归发生时是同一个结果。结构断言便宜、机器可核查，
 * 并且指向的正是那几行真正承重的代码。
 */

const surface = readFileSync(new URL('../CliTerminalSurface.vue', import.meta.url), 'utf8')
const view = readFileSync(new URL('../CopyModeView.vue', import.meta.url), 'utf8')

describe('约束①：进了复制模式，按键不许漏进 PTY', () => {
  it('【输入漏斗上有闸】—— 这才是完整的那道，keydown 那道只是第一层', () => {
    // 真机实测：只靠捕获阶段的 keydown，`jkabcgGn` 全部漏进了 shell。有好几条输入路径根本不产生
    // 可被 preventDefault 的 keydown —— CDP 的 char 事件、IME 组字、移动端软键盘、粘贴。
    // sendTerminalData 是所有输入路径的汇合点，所以闸必须在这里，而且必须在最前面。
    const fn = surface.slice(surface.indexOf('function sendTerminalData'))
    const head = fn.slice(0, fn.indexOf('ghostLastInputAt'))
    expect(head).toContain('copyModeOpen.value')
    expect(head).toContain('return')
  })

  it('监听挂在 document 的【捕获阶段】', () => {
    // xterm 的隐藏 textarea 才是终端的输入通道。冒泡阶段拦已经晚了——那时字符已经在去 PTY 的
    // 路上。第三个参数 true 是这条约束的全部实现。
    expect(view).toContain("document.addEventListener('keydown', onKeydown, true)")
  })

  it('卸载时把监听摘干净（否则退出后仍然吞键，终端变成敲不进字）', () => {
    expect(view).toContain("document.removeEventListener('keydown', onKeydown, true)")
  })

  it('【全局快捷键层让位】—— 它注册更早、同在捕获阶段，不让位就把键截在半路', () => {
    // useTabShortcuts 也挂 document 捕获阶段，且在 app 挂载时就注册（早于复制模式打开），所以它
    // 先拿到事件：leader（默认 Ctrl+B）和 findInTerminal（默认 Ctrl+F）会被 stopImmediatePropagation
    // 掉，CopyModeView 永远收不到 —— 而这两个正是 tmux copy-mode 的翻页主力（2026-09-08 实报）。
    const shortcuts = readFileSync(new URL('../../../composables/cli/useTabShortcuts.ts', import.meta.url), 'utf8')
    const fn = shortcuts.slice(shortcuts.indexOf('function handleKeydown'))
    const head = fn.slice(0, fn.indexOf('const armed'))
    expect(head).toContain('copyModeActive')
    // 让位必须是【只 return】：一旦 preventDefault，事件就到不了复制模式，等于换一种方式失效。
    expect(head.slice(head.indexOf('copyModeActive'))).not.toContain('preventDefault')
  })

  it('【surface 级监听器也让位】—— 它们只 stopPropagation，不让位就是"两个搜索框同时开"', () => {
    // findInTerminal 在 Linux 上默认 Ctrl+Shift+F，正是复制模式里的搜索键；它的 handler 只
    // stopPropagation（同节点的 CopyModeView 监听器照样收到），于是两个搜索 UI 一起冒出来。
    for (const fnName of ['function onFindShortcutKeydown', 'function onComposeShortcutKeydown']) {
      const fn = surface.slice(surface.indexOf(fnName))
      expect(fn.slice(0, fn.indexOf('\n}'))).toContain('copyModeOpen.value')
    }
  })

  it('壳把表面的 copyModeOpen 接到 adapter 上（不接 = 上面那条守卫永远是 false）', () => {
    const state = readFileSync(new URL('../../../portals/cli/useCliState.ts', import.meta.url), 'utf8')
    expect(state).toContain('copyModeActive')
    expect(state).toContain('copyModeOpen')
    expect(surface).toContain('copyModeOpen })')   // defineExpose
  })

  it('默认分支也 preventDefault + stopPropagation，而不是只处理认识的键', () => {
    // "只拦我认识的键"= 剩下的全部漏进 PTY。默认必须是吞，例外才是放行。
    const handler = view.slice(view.indexOf('function onKeydown'))
    const head = handler.slice(0, handler.indexOf('switch ('))
    expect(head).toContain('e.preventDefault()')
    expect(head).toContain('e.stopPropagation()')
  })
})

describe('tmux 的翻页手感：Ctrl 组合必须在 default 之前分流', () => {
  // 白名单式的 handler 有个安静的失败模式：没列的键既不执行也不下传，按下去和"功能坏了"
  // 无法区分。C-u/C-d/C-b/C-f 是 tmux vi copy-mode 的翻页主力，漏掉它们=大多数使用者一进来
  // 就撞墙（2026-09-08 用户实报）。
  const handler = view.slice(view.indexOf('function onKeydown'))
  const beforeSwitch = handler.slice(0, handler.indexOf('switch ('))

  it('四个键都在 switch 之前处理掉（落进 switch 就会被 default 吞）', () => {
    for (const k of ['u', 'd', 'b', 'f']) {
      expect(beforeSwitch).toContain(`k === '${k}'`)
    }
  })

  it('半页 0.5 / 整页 0.9，方向成对', () => {
    expect(beforeSwitch).toContain('pageBy(-0.5)')
    expect(beforeSwitch).toContain('pageBy(0.5)')
    expect(beforeSwitch).toContain('pageBy(-0.9)')
    expect(beforeSwitch).toContain('pageBy(0.9)')
  })

  it('C-f 让位给翻页后，搜索仍有出口（/、Cmd+F、C-S-f）', () => {
    expect(beforeSwitch).toContain('openSearch()')       // Ctrl+Shift+F
    expect(handler).toContain("case '/':")
    expect(handler).toContain('e.metaKey')               // Cmd+F
  })
})

describe('约束②：原生文本选择', () => {
  it('滚动容器显式打开 user-select（不是靠继承）', () => {
    expect(view).toContain('user-select: text')
  })

  it('没有任何 user-select: none 落在正文上（行号那种装饰除外）', () => {
    // 行号是导航装饰，复制整段时不该混进去，所以它自己是 none —— 但正文不能是。
    const textBlock = view.slice(view.indexOf('.copy-mode__text'))
    expect(textBlock.slice(0, 200)).not.toContain('user-select: none')
  })
})

describe('约束③：tmux 标签不开', () => {
  it('openCopyMode 对 tmux 标签和远程标签都拒绝，并给出【理由】', () => {
    const fn = surface.slice(surface.indexOf('function openCopyMode'))
    const body = fn.slice(0, fn.indexOf('return \'\''))
    expect(body).toContain('tmuxAttached.value')
    // 远程标签的会话住在别人机器上，历史也在那边；不拦就会打开一个永远空着的视口。
    expect(body).toContain('props.isRemote')
    // 返回的是给人看的理由，不是静默的 void —— 一个按下去毫无反应的快捷键，使用者只会以为
    // 是自己按错了。
    expect(fn.slice(0, 900)).toMatch(/return '[^']+'/)
  })

  it('关闭时把焦点还给终端（否则键盘敲下去没有接收者）', () => {
    const fn = surface.slice(surface.indexOf('function closeCopyMode'))
    expect(fn.slice(0, 400)).toContain('focus()')
  })
})

describe('实时终端在复制模式期间保持挂载', () => {
  it('复制模式是 v-if 的覆盖层，XtermTerminal 不在它的分支里', () => {
    // 卸载 xterm = 退出时要重连、要重放、位置全丢——正是这个设计要避免的"重放整段历史"。
    // lastIndexOf：模板里有具名插槽的嵌套 <template #x>，用 indexOf 会在第一个内层闭合处截断，
    // 于是这段断言会在一个只有前 5 KB 的字符串上找元素，永远找不到——一个只会自己骗自己的测试。
    const tpl = surface.slice(surface.indexOf('<template>'), surface.lastIndexOf('</template>'))
    const copyIdx = tpl.indexOf('<CopyModeView')
    const xtermIdx = tpl.indexOf('<XtermTerminal')
    expect(xtermIdx).toBeGreaterThan(-1)
    expect(copyIdx).toBeGreaterThan(-1)
    // XtermTerminal 出现在 CopyModeView 之前，且不受 copyModeOpen 控制。
    expect(xtermIdx).toBeLessThan(copyIdx)
    const xtermTag = tpl.slice(xtermIdx, tpl.indexOf('>', xtermIdx))
    expect(xtermTag).not.toContain('copyModeOpen')
  })
})

/**
 * 认证：历史请求必须走 `useCliAuth().cliFetch`（唯一带认证头、且在 401/429 时唤起认证对话框的出口）。
 *
 * 这条是**实机抓出来的**，不是设想：初版用了裸 `fetch(..., {credentials:'same-origin'})`，单测
 * （stub 掉 fetch）15 个全绿，真机一打开复制模式永远是"0 行历史"，页面上一个报错都没有。
 * 一个不带认证头的 fetch 和一个带的，长得一模一样。
 */
describe('历史请求走认证出口', () => {
  it('CliTerminalSurface 把 cliFetch 注入给 useTerminalHistory', () => {
    const call = surface.slice(surface.indexOf('useTerminalHistory('))
    expect(call.slice(0, 200)).toContain('cliFetch')
  })

  it('useTerminalHistory 自己不调裸 fetch', () => {
    const src = readFileSync(new URL('../../../composables/cli/useTerminalHistory.ts', import.meta.url), 'utf8')
    // 允许出现在注释/类型里；禁止的是真的发起一个不带认证头的请求。
    expect(src).not.toContain('await fetch(')
    expect(src).not.toContain('globalThis.fetch')
  })
})
