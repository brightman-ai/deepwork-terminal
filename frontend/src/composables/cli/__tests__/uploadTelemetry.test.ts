import { describe, expect, test } from 'bun:test'
import { createTransferMeter, formatUploadEta, formatUploadSpeed, uploadProgressPercent } from '../uploadTelemetry'

describe('upload telemetry', () => {
  test('confirmed bytes measure throughput; a stall ages speed back to zero', () => {
    let time = 0
    const meter = createTransferMeter(() => time)
    expect(meter.sample(0)).toBe(0)
    time = 1000
    expect(meter.sample(128 * 1024)).toBe(128 * 1024)
    time = 2000
    expect(meter.sample(256 * 1024)).toBe(128 * 1024)
    time = 7000
    expect(meter.sample(256 * 1024)).toBe(0)
  })
  test('progress is byte-weighted and cannot round up to 100 before all bytes arrive', () => {
    expect(uploadProgressPercent(1000, 10000)).toBe(10)
    expect(uploadProgressPercent(9999, 10000)).toBe(99)
    expect(uploadProgressPercent(10000, 10000)).toBe(100)
  })
  test('speed and ETA remain compact without invented countdowns', () => {
    expect(formatUploadSpeed(128 * 1024)).toBe('128 KB/s')
    expect(formatUploadSpeed(1.25 * 1024 * 1024)).toBe('1.3 MB/s')
    expect(formatUploadEta(null)).toBe('正在估算…')
    expect(formatUploadEta(18.1)).toBe('预计剩余 19秒')
    expect(formatUploadEta(65)).toBe('预计剩余 2分钟')
    expect(formatUploadEta(0)).toBe('即将完成')
  })
})
