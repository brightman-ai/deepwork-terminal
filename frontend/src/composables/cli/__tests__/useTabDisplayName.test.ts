import { describe, it, expect } from 'bun:test'
import { isDefaultTabName, displayTabName, tabBaseName } from '../useTabDisplayName'

describe('isDefaultTabName', () => {
  it('matches standalone\'s "终端 N" (with space) and the target "终端N" (no space) forms', () => {
    expect(isDefaultTabName('终端 1')).toBe(true)
    expect(isDefaultTabName('终端12')).toBe(true)
  })
  it('matches pro\'s bare "终端" default (useCliV2.createSession posts name=\'终端\')', () => {
    expect(isDefaultTabName('终端')).toBe(true)
  })
  it('rejects a user-chosen custom name', () => {
    expect(isDefaultTabName('部署脚本')).toBe(false)
    expect(isDefaultTabName('终端 1 - 备份')).toBe(false)
    expect(isDefaultTabName('终端部署')).toBe(false)
  })
})

describe('displayTabName', () => {
  it('renders an untouched default-named tab as its LIVE position, not the frozen creation-time name', () => {
    // Tab was created as "终端 3" (3rd by creation order) but now sits at visible position 1
    // (earlier tabs closed) — the label must track position, matching the Alt+1 shortcut.
    expect(displayTabName('终端 3', 1)).toBe('终端1')
  })
  it('numbers pro\'s otherwise-indistinguishable bare "终端" tabs by position', () => {
    // The screenshot bug: two pro tabs both read "终端" with nothing to tell them apart.
    expect(displayTabName('终端', 1)).toBe('终端1')
    expect(displayTabName('终端', 2)).toBe('终端2')
  })
  it('leaves a user-renamed tab untouched regardless of position', () => {
    expect(displayTabName('部署脚本', 2)).toBe('部署脚本')
  })
  it('falls back to the stored name when position is unknown (tab not in the visible set)', () => {
    expect(displayTabName('终端 1', undefined)).toBe('终端 1')
  })
})

/**
 * tabBaseName —— 给自己另外画位置角标的地方用（标签栏）。
 *
 * 编号只该有一个落点。此前默认名渲染成「终端3」、而改过名的「build」什么编号都没有 —— 同一个信息
 * 放在两个不同位置、还不总是都在，而 Alt+N / leader+N 正是按这个号切的。标签栏因此改成
 * 「名字一律不带编号 + 角标一律带」。
 */
describe('tabBaseName', () => {
  it('把默认名里的编号摘掉，只留「终端」 —— 号交给角标去说', () => {
    expect(tabBaseName('终端 3')).toBe('终端')
    expect(tabBaseName('终端12')).toBe('终端')
    expect(tabBaseName('终端')).toBe('终端')   // pro 的裸默认名
  })

  it('用户自己起的名字一个字都不动', () => {
    expect(tabBaseName('部署脚本')).toBe('部署脚本')
    expect(tabBaseName('终端 1 - 备份')).toBe('终端 1 - 备份')
  })

  // 和 displayTabName 是**互补**不是替代：没有角标的地方（总览卡片、右键菜单、底栏提示）名字是
  // 编号的唯一载体，摘掉就真找不回来了。两个函数各有各的落点，别互相取代。
  it('displayTabName 仍然带编号 —— 它服务的是没有角标的地方', () => {
    expect(displayTabName('终端 3', 1)).toBe('终端1')
    expect(tabBaseName('终端 3')).toBe('终端')
  })
})
