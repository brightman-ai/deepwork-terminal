import { afterAll, describe, expect, it } from 'bun:test'

// ── 「tmux server 没了」必须能到达眼睛 ────────────────────────────────────────────────────────
//
// pane bar 的门是 `attached && windows.length`。2026-08-08 19:34 一台跑了十一天的 tmux server
// 吃了 SIGSEGV（control_notify_client_detached → control_write 里的空指针），于是这条门自然为假，
// 整条 bar 消失 —— 在屏幕上和「这个 shell 本来就不在 tmux 里」一模一样。全程序唯一的痕迹是一行
// INFO，使用者是自己敲 `tmux attach` 读到 "no sessions" 才知道的。
//
// 服务端现在把这个事实说出来（serverVanished）。只有它知道得了：这个标志跨页面刷新、跨重连、
// 跨「事发时根本没人在看」都成立，而前端侧的「它刚才还在」活不过一次 F5。这里钉住前端那一半。

const localStorageStub = { getItem: () => null, setItem: () => {}, removeItem: () => {} }
const originalWindow = (globalThis as any).window
const originalLocalStorage = (globalThis as any).localStorage

// useTmuxState 的 import 图里有 useCliAuth，它在模块加载期就读 window.location —— 照本仓库
// 既有约定（cli-paste-resolver.test.ts）在 DOM-less 的 bun runtime 下补一个最小 window。
Object.defineProperty(globalThis, 'localStorage', { value: localStorageStub, configurable: true })
Object.defineProperty(globalThis, 'window', {
  value: {
    location: { search: '', pathname: '/', hash: '' },
    history: { replaceState: () => {} },
    localStorage: localStorageStub,
  },
  configurable: true,
})

afterAll(() => {
  delete (globalThis as any).window
  delete (globalThis as any).localStorage
  if (originalWindow !== undefined) (globalThis as any).window = originalWindow
  if (originalLocalStorage !== undefined) (globalThis as any).localStorage = originalLocalStorage
})

async function store(id: string) {
  const { useTmuxState } = await import('../useTmuxState')
  return useTmuxState(() => id)
}

describe('tmux serverVanished', () => {
  it('server 消失时前端拿得到这个事实', async () => {
    const s = await store('vanish-a')
    s.handleWSMessage({ installed: true, serverRunning: false, serverVanished: true, attached: false, sessions: [] })
    expect(s.serverVanished.value).toBe(true)
    // 而 pane bar 那条门确实是关的 —— 正因为它关着，才必须有别的东西替它说话。
    expect(s.attached.value).toBe(false)
    expect(s.windows.value.length).toBe(0)
  })

  it('从不用 tmux 的人永远不会看到这条', async () => {
    const s = await store('vanish-b')
    // 没装 / 从没开过：serverRunning 同样是 false，但那不是「它死了」。
    s.handleWSMessage({ installed: false, serverRunning: false, attached: false, sessions: [] })
    expect(s.serverVanished.value).toBe(false)
    s.handleWSMessage({ installed: true, serverRunning: false, attached: false, sessions: [] })
    expect(s.serverVanished.value).toBe(false)
    // 这条断言的价值全在这里：误报一次，这行字以后就再也没人信了。
  })

  it('server 回来这条自己消失，不需要谁去关它', async () => {
    const s = await store('vanish-c')
    s.handleWSMessage({ installed: true, serverRunning: false, serverVanished: true, attached: false, sessions: [] })
    expect(s.serverVanished.value).toBe(true)
    s.handleWSMessage({
      installed: true, serverRunning: true, attached: true, attachedSession: 'main',
      sessions: [{ name: 'main', attached: true, windows: [{ index: 1, name: 'w', active: true, panes: [] }] }],
    })
    expect(s.serverVanished.value).toBe(false)
    expect(s.windows.value.length).toBe(1)
  })

  it('帧里没有这个键 = 没有这回事（旧服务端向后兼容）', async () => {
    const s = await store('vanish-d')
    s.handleWSMessage({ installed: true, serverRunning: true, attached: true, sessions: [] })
    expect(s.serverVanished.value).toBe(false)
  })
})
