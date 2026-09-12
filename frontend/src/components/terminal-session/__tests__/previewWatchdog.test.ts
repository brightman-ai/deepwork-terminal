import { describe, it, expect } from 'bun:test'
import { armRenderWatchdog } from '../previewWatchdog'

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms))

describe('previewWatchdog（失败必须可见）', () => {
  it('超时未 settle → 报人话文案，含下载逃生口', async () => {
    let msg = ''
    armRenderWatchdog('pdf', (m) => { msg = m }, 30)
    await sleep(120)
    expect(msg).toContain('pdf')
    expect(msg).toContain('下载')
  })

  it('settle 后不触发（正常渲染路径零打扰）', async () => {
    let fired = false
    const wd = armRenderWatchdog('docx', () => { fired = true }, 30)
    wd.settle()
    await sleep(120)
    expect(fired).toBe(false)
  })

  it('dispose 后不触发（组件卸载/重载不泄漏定时器）', async () => {
    let fired = false
    const wd = armRenderWatchdog('docx', () => { fired = true }, 30)
    wd.dispose()
    await sleep(120)
    expect(fired).toBe(false)
  })

  it('超时触发后再 settle 是无害的（迟到的成功不回调第二次）', async () => {
    let count = 0
    const wd = armRenderWatchdog('xlsx', () => { count++ }, 30)
    await sleep(120)
    wd.settle()
    expect(count).toBe(1)
  })
})
