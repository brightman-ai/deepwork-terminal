import { describe, it, expect } from 'bun:test'
import { isViewer, viewportIntent } from '../viewportDeclaration'

describe('isViewer', () => {
  it('要同时满足：页面在前台，且这个标签是选中的那个', () => {
    expect(isViewer({ pageVisible: true, surfaceActive: true })).toBe(true)
    expect(isViewer({ pageVisible: false, surfaceActive: true })).toBe(false)
    // 每个标签的 surface 都常驻挂载（v-show），所以"页面在前台"完全推不出"看的是我"。
    expect(isViewer({ pageVisible: true, surfaceActive: false })).toBe(false)
    expect(isViewer({ pageVisible: false, surfaceActive: false })).toBe(false)
  })
})

describe('viewportIntent', () => {
  // A5：后台的手机页面不许压扁正在用的 PC。
  // 缺陷原样：声明挂在连接生命周期上——WS 一连上就阶梯式声明三次（100/500/1500ms）。
  // 躺在后台的页面网络一抖、WS 重连，就连喊三声"我是最新的"。
  it('不是观看者 → 绝不声明，哪怕几何真的变了', () => {
    expect(viewportIntent(false, false, true)).toBe('silent')
    expect(viewportIntent(false, false, false)).toBe('silent')
  })

  // 服务端改成"所有观看窗口取最小值"之后新增的一条，也是整套机制能成立的前提：
  // 只能声明不能撤回的最小值是个棘轮。后台挂着的手机那 60 列会永远压着 PC，
  // 而它自己什么都没在显示。
  it('刚刚不再是观看者 → 必须撤回，不是闭嘴', () => {
    expect(viewportIntent(true, false, true)).toBe('withdraw')
    expect(viewportIntent(true, false, false)).toBe('withdraw')
    // 撤回只在边沿发生一次；之后重复喊改变不了任何事实。
    expect(viewportIntent(false, false, false)).toBe('silent')
  })

  // A4：看回来的人必须拿到自己的排版。这条最反直觉，也最容易被"优化"掉。
  // 现在还多了一层理由：上一次离开时我撤回过，服务端那边我这一票是 0，
  // "我的网格没变"因此完全推不出"服务端知道我多大"。
  it('刚成为观看者 → 必须声明，哪怕本地网格一个像素都没变', () => {
    expect(viewportIntent(false, true, false)).toBe('declare')
    expect(viewportIntent(false, true, true)).toBe('declare')
  })

  it('一直在看 → 只有真的变了才说', () => {
    expect(viewportIntent(true, true, true)).toBe('declare')
    // 没变还说 = 一帧买不到任何东西的流量，而且服务端会把尺寸变化广播给所有 attach 的
    // 客户端，等于让别人也白醒一次。
    expect(viewportIntent(true, true, false)).toBe('silent')
  })

  // 走一遍真实序列：PC 在用 → 手机切前台接管 → 手机切后台 → 手机后台重连 → 手机再切回来。
  it('端到端序列：进场声明、离场撤回、后台不抢、回来必接管', () => {
    let was = false
    const step = (visible: boolean, active: boolean, geom: boolean) => {
      const now = isViewer({ pageVisible: visible, surfaceActive: active })
      const intent = viewportIntent(was, now, geom)
      was = now
      return intent
    }

    expect(step(true, true, false)).toBe('declare') // 手机切到前台 → 参与裁决
    expect(step(true, true, false)).toBe('silent') // 还在看，什么都没变 → 闭嘴
    expect(step(false, true, false)).toBe('withdraw') // 切到后台 → 交还这一票
    expect(step(false, true, true)).toBe('silent') // 后台里 WS 重连 + fit → 仍然闭嘴（A5）
    expect(step(true, true, false)).toBe('declare') // 回到前台 → 重新参与（A4）
  })
})
