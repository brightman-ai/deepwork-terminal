import { describe, it, expect } from 'bun:test'
import { readFileSync } from 'node:fs'

/**
 * 模板里调用的每个函数，都必须真的在 `<script setup>` 里存在。
 *
 * ## 为什么需要这个测试
 *
 * 2026-08-22 实测：把 `officialLabel` 改名成 `accountLabel` 后，模板里漏了一处调用点。
 * `vue-tsc --noEmit` **一声不吭地通过了**——把调用点换成 `thisFunctionDoesNotExist` 再跑，
 * 它照样零输出。也就是说这一类错误（模板引用了不存在的绑定）在本工程**没有任何静态门拦得住**，
 * 只会在运行时炸成 `TypeError: z.officialLabel is not a function`，而 Vue 把出错的子树渲染成
 * 一个空注释 `<!---->`：**页面上那一块直接消失，控制台之外毫无痕迹**。
 *
 * 单测绿、类型绿、构建绿，功能没了——这正是那类最贵的静默失败。所以这里用最笨但真的有效的办法：
 * 从源码里抓模板中的函数调用，逐个回查脚本区有没有这个名字。
 *
 * 反向验证过：把任一调用点改成不存在的名字，本测试立刻变红。
 */

const SOURCE = readFileSync(new URL('../UsageChip.vue', import.meta.url), 'utf8')

/** JS/浏览器/Vue 模板里合法但不在脚本区声明的名字。 */
const AMBIENT = new Set([
  'Math', 'Number', 'String', 'Boolean', 'Array', 'Object', 'JSON', 'Date', 'parseInt', 'parseFloat',
  'isNaN', 'isFinite', 'encodeURIComponent', 'decodeURIComponent', 'Intl', 'Set', 'Map',
  // 模板语法本身
  'if', 'for', 'in', 'of', 'return', 'typeof', 'new', 'catch', 'switch', 'while', 'function',
])

function sectionOf(tag: 'template' | 'script'): string {
  // 顶层块：取第一个 <tag ...> 到最后一个 </tag>
  const open = SOURCE.indexOf(`<${tag}`)
  const close = SOURCE.lastIndexOf(`</${tag}>`)
  expect(open).toBeGreaterThan(-1)
  expect(close).toBeGreaterThan(open)
  return SOURCE.slice(open, close)
}

describe('UsageChip 模板绑定', () => {
  it('模板里调用的每个函数都在脚本区有定义（vue-tsc 拦不住这一类）', () => {
    const template = sectionOf('template')
    const script = sectionOf('script')

    // 只看插值 {{ … }} 与指令值 :attr="…" / @evt="…" 里的调用，避免把 HTML 文本当代码。
    const expressions: string[] = []
    for (const m of template.matchAll(/\{\{([\s\S]*?)\}\}/g)) expressions.push(m[1])
    for (const m of template.matchAll(/\s(?::|v-if=|v-else-if=|v-for=|v-show=|@)[\w.:-]*="([^"]*)"/g)) expressions.push(m[1])

    const called = new Set<string>()
    for (const expr of expressions) {
      // 跳过成员调用（a.b()）——那是对象方法，不是脚本区绑定。
      for (const m of expr.matchAll(/(^|[^\w.$])([a-zA-Z_$][\w$]*)\s*\(/g)) called.add(m[2])
    }

    const missing = [...called]
      .filter((name) => !AMBIENT.has(name))
      .filter((name) => !new RegExp(`(?:function|const|let|var)\\s+${name}\\b|\\b${name}\\s*[,}].*=\\s*use|\\b${name}\\s*:`).test(script))
      .filter((name) => !new RegExp(`\\b${name}\\b`).test(script)) // 兜底：解构/导入进来的也算数

    expect(missing).toEqual([])
    // 这个测试必须真的在看东西——空集合通过是无意义的绿。
    expect(called.size).toBeGreaterThan(5)
  })
})
