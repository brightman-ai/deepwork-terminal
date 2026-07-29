import { describe, it, expect } from 'bun:test'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'

/**
 * 机械门：闸门被删掉要能立刻变红。
 *
 * canMeasureTerminal 自己的单测（terminalFit.test.ts）只证明这个**谓词**算得对，证明不了它
 * 被**装在了该装的地方**——而这个缺陷的全部要害正是位置：pro 那份 TerminalSurface 的注释
 * ("tab 切换后重新 fit，消除 v-show 隐藏时无法测量尺寸的问题") 说明这件事早就有人知道，
 * 但补的是症状（切回来再量一次），隐藏中那次测量照跑不误，10×6 照发进 PTY。
 *
 * 所以这里按源码钉死三个调用点。它不优雅，但它是唯一能让"闸门被顺手删掉"变红的检查——
 * 而这类删除在别的测试里是**完全静默**的：单测全绿、类型全过、控制台无报错，只有真实用户
 * 在高延迟链路上看见那根窄条。
 */
const SRC = readFileSync(
  join(import.meta.dir, '..', 'XtermTerminal.vue'),
  'utf8',
)

describe('XtermTerminal 的不可测量闸门', () => {
  it('导入了 canMeasureTerminal', () => {
    expect(SRC).toContain("from './terminalFit'")
    expect(SRC).toContain('canMeasureTerminal')
  })

  it('ResizeObserver 回调里带闸门 —— 隐藏瞬间那次 0×0 回调正是最危险的一次', () => {
    const ro = SRC.slice(SRC.indexOf('resizeObserver = new ResizeObserver'))
    const body = ro.slice(0, ro.indexOf('resizeObserver.observe'))
    expect(body).toContain('canMeasureTerminal')
    // 闸门必须挡在 emit('resize') 之前，否则隐藏中的尺寸照样上报给服务端。
    expect(body.indexOf('canMeasureTerminal')).toBeLessThan(body.indexOf("emit('resize'"))
  })

  it('对外暴露的 fit() 带闸门 —— 所有外部重排路径都走它', () => {
    const fn = SRC.slice(SRC.indexOf('function fit()'))
    const body = fn.slice(0, fn.indexOf('\n}'))
    expect(body).toContain('canMeasureTerminal')
  })

  it('创建时的首次 fit 带闸门 —— 隐藏中诞生的终端不该被量成 10 列', () => {
    const init = SRC.slice(SRC.indexOf('function initTerminal()'))
    const upToFirstFit = init.slice(0, init.indexOf('fitAddon.fit()'))
    expect(upToFirstFit).toContain('canMeasureTerminal')
  })
})
