import { describe, it, expect } from 'bun:test'
import { resolveRenderer, rendererDeclineReason } from '../terminalRenderer'

describe('resolveRenderer', () => {
  it('移动端默认 DOM —— iOS 上 WebGL 字形图集会把 e 画成 ョ（Human 实测截图）', () => {
    expect(resolveRenderer({ override: null, isMobile: true })).toBe('dom')
  })

  it('桌面默认仍是 WebGL —— 那里的 3× 收益是实测的，且没有一例串字报告', () => {
    expect(resolveRenderer({ override: null, isMobile: false })).toBe('webgl')
  })

  // 这一条是整个模块存在的理由：v0.8.0 把 WebGL 设成无条件默认，却没留任何出口，
  // 于是「桌面到底有没有这个 bug」只能靠猜。开关就是把争论变成实验的那个东西。
  it('显式 override 两个方向都压过默认', () => {
    expect(resolveRenderer({ override: 'dom', isMobile: false })).toBe('dom')
    expect(resolveRenderer({ override: 'webgl', isMobile: true })).toBe('webgl')
  })
})

describe('rendererDeclineReason', () => {
  it('拿到 WebGL 就没有「为什么没拿到」可说', () => {
    expect(rendererDeclineReason({ override: null, isMobile: false })).toBe('')
    expect(rendererDeclineReason({ override: 'webgl', isMobile: true })).toBe('')
  })

  it('区分「你自己指定的」和「我们替你定的」—— 前者别显得像故障', () => {
    expect(rendererDeclineReason({ override: 'dom', isMobile: false })).toContain('?renderer=dom')
    expect(rendererDeclineReason({ override: null, isMobile: true })).toContain('移动端默认')
  })
})
