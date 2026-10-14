import { describe, it, expect } from 'bun:test'
import {
  COMPOSER_MARGIN,
  clampPlacement,
  cursorRowTop,
  defaultPlacement,
  parsePlacement,
  placementStorageKey,
  serializePlacement,
} from '../composePlacement'

const bounds = { w: 1000, h: 600 }
const box = { w: 400, h: 120 }

describe('clampPlacement — 框永远落在终端容器里', () => {
  it('已经在里面：原样', () => {
    expect(clampPlacement({ x: 100, y: 100 }, box, bounds)).toEqual({ x: 100, y: 100 })
  })

  it('越过四边：被夹回并留出边距', () => {
    expect(clampPlacement({ x: -50, y: -50 }, box, bounds)).toEqual({ x: COMPOSER_MARGIN, y: COMPOSER_MARGIN })
    expect(clampPlacement({ x: 9999, y: 9999 }, box, bounds)).toEqual({
      x: bounds.w - box.w - COMPOSER_MARGIN,
      y: bounds.h - box.h - COMPOSER_MARGIN,
    })
  })

  it('容器比框还小：贴左上，不出现负坐标', () => {
    expect(clampPlacement({ x: 300, y: 300 }, { w: 500, h: 500 }, { w: 200, h: 200 })).toEqual({
      x: COMPOSER_MARGIN, y: COMPOSER_MARGIN,
    })
  })
})

describe('defaultPlacement — REQ-TI-06：默认出现在光标所在行', () => {
  it('框的上沿对齐光标行的上沿', () => {
    expect(defaultPlacement(200, box, bounds)).toEqual({ x: COMPOSER_MARGIN, y: 200 })
  })

  it('光标行靠近底部：框的下沿贴底，上沿随框高度往上让', () => {
    const p = defaultPlacement(580, box, bounds)
    expect(p.y).toBe(bounds.h - box.h - COMPOSER_MARGIN)
    expect(p.y + box.h).toBeLessThanOrEqual(bounds.h - COMPOSER_MARGIN)
  })

  it('框长高了（多打几行）：同一个锚点，位置随之上移，始终在容器内', () => {
    const tall = { w: 400, h: 400 }
    expect(defaultPlacement(300, tall, bounds).y).toBe(bounds.h - tall.h - COMPOSER_MARGIN)
  })

  it('光标不在可见区（null）：贴终端底部', () => {
    expect(defaultPlacement(null, box, bounds)).toEqual({
      x: COMPOSER_MARGIN,
      y: bounds.h - box.h - COMPOSER_MARGIN,
    })
  })
})

describe('cursorRowTop — 光标所在行的上沿', () => {
  const g = { baseY: 0, cursorY: 10, viewportY: 0, rows: 40, screenOffsetTop: 4, screenHeight: 800 }

  it('视口贴底、光标在第 10 行：4 + 10 × (800/40)', () => {
    expect(cursorRowTop(g)).toBe(4 + 10 * 20)
  })

  it('有 scrollback：光标绝对行 = baseY + cursorY，视口贴底时相对行仍是 cursorY', () => {
    expect(cursorRowTop({ ...g, baseY: 300, viewportY: 300 })).toBe(4 + 10 * 20)
  })

  it('用户往回翻了，光标已经滚出可见区 → null（框贴底，不指向一个不存在的行）', () => {
    expect(cursorRowTop({ ...g, baseY: 300, viewportY: 100 })).toBeNull()
  })

  it('往回翻但光标仍在视口里 → 仍能算（按它在视口里的相对行）', () => {
    // 绝对行 = 30 + 35 = 65；视口 [50, 90)：光标在视口第 15 行
    expect(cursorRowTop({ ...g, baseY: 30, cursorY: 35, viewportY: 50 })).toBe(4 + 15 * 20)
  })

  it('量不到（高度 0 / 行数 0）→ null', () => {
    expect(cursorRowTop({ ...g, screenHeight: 0 })).toBeNull()
    expect(cursorRowTop({ ...g, rows: 0 })).toBeNull()
  })
})

describe('保存的位置（按 tab、跨刷新）', () => {
  it('往返一致（取整）', () => {
    expect(parsePlacement(serializePlacement({ x: 12.4, y: 99.6 }))).toEqual({ x: 12, y: 100 })
  })

  for (const raw of [null, '', 'x', '{}', '{"x":"1","y":2}', '{"x":null,"y":1}', '[1,2]', 'null']) {
    it(`坏数据 ${JSON.stringify(raw)} 当没有`, () => {
      expect(parsePlacement(raw)).toBeNull()
    })
  }

  it('键按 tab 区分，两个标签互不覆盖', () => {
    expect(placementStorageKey('abc')).toBe('cli-compose-pos:abc')
    expect(placementStorageKey('abc')).not.toBe(placementStorageKey('def'))
    expect(placementStorageKey(undefined)).toBe('cli-compose-pos')
  })

  it('已保存的位置在窗口缩小后被夹回可见区，但保存的值本身不被改写', () => {
    const saved = { x: 500, y: 400 }
    const small = { w: 600, h: 400 }
    const shown = clampPlacement(saved, box, small)
    expect(shown.x + box.w).toBeLessThanOrEqual(small.w)
    expect(shown.y + box.h).toBeLessThanOrEqual(small.h)
    expect(saved).toEqual({ x: 500, y: 400 })
    expect(clampPlacement(saved, box, bounds)).toEqual(saved) // 窗口放回去就复原
  })
})
