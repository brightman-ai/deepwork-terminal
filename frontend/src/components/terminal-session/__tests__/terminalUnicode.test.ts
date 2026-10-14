import { describe, expect, test } from 'bun:test'
import { readFileSync } from 'node:fs'
import { Terminal } from '@xterm/xterm'
import { TERMINAL_UNICODE_VERSION, enableUnicode11 } from '../terminalUnicode'

// 真实的 xterm Terminal（不 open，不需要 DOM）：量的就是 xterm 自己的宽度表，不是我们对它的假设。
// allowProposedApi 与 XtermTerminal.vue 里一致 —— terminal.unicode 是提议 API，没有它读写都会抛。
//
// 构造一个 Terminal 要几百毫秒（机器忙时更久），所以全文件只造两个、放在模块级（不占单个用例的超时），
// 每次测量前清屏归位。
const makeTerminal = (): Terminal => new Terminal({ cols: 40, rows: 3, allowProposedApi: true })
const plain = makeTerminal()
const upgraded = makeTerminal()
enableUnicode11(upgraded)

/** 清屏归位，写一个字符再写一个 X：光标列数减一 = 这个字符占的格数。 */
async function cellsOf(term: Terminal, s: string): Promise<number> {
  await new Promise<void>((resolve) => term.write(`\x1b[2J\x1b[H${s}X`, resolve))
  return term.buffer.active.cursorX - 1
}

describe('终端按 Unicode 11 量宽度', () => {
  test('版本号钉死为 11', () => {
    expect(TERMINAL_UNICODE_VERSION).toBe('11')
    expect(upgraded.unicode.activeVersion).toBe('11')
  })

  test('不装的话是 6 —— 这就是 emoji 错位的来源', async () => {
    expect(plain.unicode.activeVersion).toBe('6')
    for (const e of ['🤖', '💰', '🧠']) expect(await cellsOf(plain, e)).toBe(1)
  })

  test('装了之后 emoji 是 2 格，和程序排版时的假设一致', async () => {
    for (const e of ['🤖', '💰', '🧠', '✅', '❌', '😀']) expect(await cellsOf(upgraded, e)).toBe(2)
  })

  test('宽度表以外的字符不受影响：ASCII 1 格、CJK 2 格（装与不装一样）', async () => {
    for (const term of [plain, upgraded]) {
      expect(await cellsOf(term, 'a')).toBe(1)
      expect(await cellsOf(term, '你')).toBe(2)
    }
  })

  // 诚实的边界：Unicode 11 表不管默认文本呈现的符号。Claude Code 状态行里的 ⏺ ✻ ⚠️ 仍是 1 格，
  // 要靠字素簇宽度（unicode-graphemes 插件）才能改变。钉住它，是为了不让人以为这一步已经把它们修了。
  test('⏺ ✻ ⚠️ 在 Unicode 11 里仍是 1 格（这一步管不到它们）', async () => {
    for (const s of ['⏺', '✻', '⚠️']) expect(await cellsOf(upgraded, s)).toBe(1)
  })
})

describe('XtermTerminal.vue 真的在第一次写入之前启用了它', () => {
  const src = readFileSync(new URL('../XtermTerminal.vue', import.meta.url), 'utf8')

  test('先于 open() 调用 enableUnicode11，晚于 new Terminal', () => {
    const created = src.indexOf('terminal = new Terminal(')
    const enabled = src.indexOf('enableUnicode11(terminal)')
    const opened = src.indexOf('terminal.open(terminalContainer.value)')
    expect(created).toBeGreaterThan(-1)
    expect(enabled).toBeGreaterThan(created)
    expect(opened).toBeGreaterThan(enabled)
  })

  test('Terminal 构造时开着 allowProposedApi（terminal.unicode 是提议 API）', () => {
    const ctor = src.slice(src.indexOf('terminal = new Terminal('), src.indexOf('terminal.parser.registerOscHandler'))
    expect(ctor).toContain('allowProposedApi: true')
  })

  test('write() 先走 initTerminal()，所以第一次写入一定晚于上面的配置', () => {
    const write = src.slice(src.indexOf('function write('), src.indexOf('terminal?.write(data'))
    expect(write).toContain('initTerminal()')
  })
})
