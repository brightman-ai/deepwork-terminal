import { describe, it, expect } from 'bun:test'
import { isViewer, shouldDeclareViewport } from '../viewportDeclaration'

describe('isViewer', () => {
  it('要同时满足：页面在前台，且这个标签是选中的那个', () => {
    expect(isViewer({ pageVisible: true, surfaceActive: true })).toBe(true)
    expect(isViewer({ pageVisible: false, surfaceActive: true })).toBe(false)
    // 每个标签的 surface 都常驻挂载（v-show），所以"页面在前台"完全推不出"看的是我"。
    expect(isViewer({ pageVisible: true, surfaceActive: false })).toBe(false)
    expect(isViewer({ pageVisible: false, surfaceActive: false })).toBe(false)
  })
})

describe('shouldDeclareViewport', () => {
  // A5：后台的手机页面不许压扁正在用的 PC。
  // 缺陷原样：声明挂在连接生命周期上——WS 一连上就阶梯式声明三次（100/500/1500ms）。
  // 躺在后台的页面网络一抖、WS 重连，就连喊三声"我是最新的"。
  it('不是观看者 → 一个字都不许说，哪怕几何真的变了', () => {
    expect(shouldDeclareViewport(false, false, true)).toBe(false)
    expect(shouldDeclareViewport(true, false, true)).toBe(false)
    expect(shouldDeclareViewport(false, false, false)).toBe(false)
  })

  // A4：看回来的人必须拿到自己的排版。这条最反直觉，也最容易被"优化"掉。
  // 缺陷原样：切回前台只做整屏重绘，不声明尺寸，理由是"我的网格又没变"。
  // 那句话推不出"服务端记的还是我的网格"——那份尺寸是共享的，我不看的时候别人改了。
  it('刚成为观看者 → 必须声明，哪怕本地网格一个像素都没变', () => {
    expect(shouldDeclareViewport(false, true, false)).toBe(true)
    expect(shouldDeclareViewport(false, true, true)).toBe(true)
  })

  it('一直在看 → 只有真的变了才说', () => {
    expect(shouldDeclareViewport(true, true, true)).toBe(true)
    // 没变还说 = 每次重连、每次 tick 都对着别人喊"我是最新的"。
    expect(shouldDeclareViewport(true, true, false)).toBe(false)
  })

  // 走一遍真实序列：PC 在用 → 手机切前台接管 → 手机切后台 → 手机后台重连 → 手机再切回来。
  it('端到端序列：交接干净，后台不抢，回来必接管', () => {
    let was = false
    const step = (visible: boolean, active: boolean, geom: boolean): boolean => {
      const now = isViewer({ pageVisible: visible, surfaceActive: active })
      const declare = shouldDeclareViewport(was, now, geom)
      was = now
      return declare
    }

    expect(step(true, true, false)).toBe(true)    // 手机切到前台 → 接管排版
    expect(step(true, true, false)).toBe(false)   // 还在看，什么都没变 → 闭嘴
    expect(step(false, true, false)).toBe(false)  // 切到后台 → 闭嘴
    expect(step(false, true, true)).toBe(false)   // 后台里 WS 重连 + fit → 仍然闭嘴（A5）
    expect(step(true, true, false)).toBe(true)    // 回到前台 → 再次接管（A4）
  })
})
