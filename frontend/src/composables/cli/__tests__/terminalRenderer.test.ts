import { describe, it, expect, afterEach } from 'bun:test'
import {
  resolveRenderer, DEFAULT_RENDERER, setRendererPreference, rendererPin, rendererPreference,
} from '../terminalRenderer'

describe('resolveRenderer — 钉 > 偏好 > 默认', () => {
  it('谁也没发话 → DOM。默认站在"画错了也看不出来"的对立面（见文件头）', () => {
    expect(resolveRenderer({ pin: null, preference: null }).renderer).toBe('dom')
    expect(DEFAULT_RENDERER).toBe('dom')
  })

  it('使用者偏好压过默认，并被标为 chosen —— 面板据此说"记住了"', () => {
    const d = resolveRenderer({ pin: null, preference: 'webgl' })
    expect(d.renderer).toBe('webgl')
    expect(d.source).toBe('chosen')
  })

  // 诊断钉是用来做对照实验的：同一个终端两种渲染器各开一个标签页直接比。
  // 它必须压过偏好，否则实验条件被偏好悄悄改写，比较出来的结论是假的。
  it('本标签页的诊断钉压过偏好，两个方向都压', () => {
    expect(resolveRenderer({ pin: 'dom', preference: 'webgl' })).toEqual({ renderer: 'dom', source: 'pinned' })
    expect(resolveRenderer({ pin: 'webgl', preference: 'dom' })).toEqual({ renderer: 'webgl', source: 'pinned' })
  })

  // 这一条是整个模块存在的理由：v0.8.0 把 WebGL 设成无条件默认，却没留任何出口，
  // 于是「桌面到底有没有这个 bug」只能靠猜。开关就是把争论变成实验的那个东西。
  // 包括"就在这台手机上给我 WebGL" —— 提这个要求的人正是在复现那个 bug。
  it('显式意愿两个方向都压过默认', () => {
    expect(resolveRenderer({ pin: 'webgl', preference: null }).renderer).toBe('webgl')
    expect(resolveRenderer({ pin: null, preference: 'dom' }).renderer).toBe('dom')
  })
})

/** 最小 Storage 替身 —— 只需要 get/set/remove 三个动作，不值得拉一整个 DOM 实现进来。 */
function fakeStorage(): Storage {
  const m = new Map<string, string>()
  return {
    getItem: (k: string) => m.get(k) ?? null,
    setItem: (k: string, v: string) => { m.set(k, v) },
    removeItem: (k: string) => { m.delete(k) },
    clear: () => m.clear(),
    key: () => null,
    get length() { return m.size },
  } as unknown as Storage
}

const realWindow = (globalThis as { window?: unknown }).window
afterEach(() => { (globalThis as { window?: unknown }).window = realWindow })

/** 地址栏替身：href 与 search 一起走，因为拔钉要同时改到这两者。 */
function installWindow(search = ''): void {
  const loc = { href: `https://host/app${search}`, search }
  ;(globalThis as { window?: unknown }).window = {
    location: loc,
    localStorage: fakeStorage(),
    sessionStorage: fakeStorage(),
    history: {
      state: null,
      replaceState: (_s: unknown, _t: string, url: string) => {
        const u = new URL(url)
        loc.href = u.toString()
        loc.search = u.search
      },
    },
  }
}

describe('setRendererPreference', () => {
  it('写偏好，且跨标签页存活的那一层才是它落脚的地方', () => {
    installWindow()
    setRendererPreference('webgl')
    expect(rendererPreference()).toBe('webgl')
  })

  // 这一条是这个函数存在的理由，也是它最容易被写漏的一行。
  // 钉压过偏好；不清钉，一个几天前用过 ?renderer=dom 的标签页里，按钮点下去、页面刷新、
  // 渲染器纹丝不动 —— 一个看得见却无声失效的开关，比没有开关更糟。
  // 钉存在于两处：sessionStorage，以及地址栏里那个查询参数本身。
  // 只清前者是第一版真实犯过的错，而且**正是这条断言当场抓住的**：重载后地址栏那个
  // ?renderer=dom 会原封不动地再被读一遍、再写回 sessionStorage，切换于是无声失效。
  it('两处一起拔：sessionStorage 里的钉，和地址栏里那个会让它复活的查询参数', () => {
    installWindow('?renderer=dom')
    expect(rendererPin()).toBe('dom')           // 钉已落在 sessionStorage 里

    setRendererPreference('webgl')

    expect(rendererPin()).toBe(null)            // 两处都没了 —— 重载也复活不了
    expect((globalThis as { window: { location: { search: string } } }).window.location.search)
      .not.toContain('renderer')
    expect(resolveRenderer({ pin: rendererPin(), preference: rendererPreference() }))
      .toEqual({ renderer: 'webgl', source: 'chosen' })
  })

  it('地址栏里别的参数不受连累 —— 只摘 renderer 这一个', () => {
    installWindow('?code=abc&renderer=dom&render_sync=0')
    setRendererPreference('webgl')
    const search = (globalThis as { window: { location: { search: string } } }).window.location.search
    expect(search).toContain('code=abc')
    expect(search).toContain('render_sync=0')
    expect(search).not.toContain('renderer=')
  })

  it('存储被禁（隐私模式）不抛异常 —— 换不了渲染器不该把终端一起拖下水', () => {
    ;(globalThis as { window?: unknown }).window = {
      location: { search: '' },
      get localStorage(): Storage { throw new Error('denied') },
      get sessionStorage(): Storage { throw new Error('denied') },
    }
    expect(() => setRendererPreference('webgl')).not.toThrow()
    expect(rendererPreference()).toBe(null)
    expect(rendererPin()).toBe(null)
  })
})
