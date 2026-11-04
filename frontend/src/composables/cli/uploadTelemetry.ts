/** A short rolling window reacts to stalls and rate changes; timestamps are
 * monotonic and bytes count payload ACKs, not multipart framing. */
export function createTransferMeter(now = () => performance.now()) {
  let samples: { at: number; bytes: number }[] = []
  return {
    sample(bytes: number): number {
      const at = now()
      if (!samples.length || bytes < samples[samples.length - 1].bytes) samples = [{ at, bytes }]
      else if (at > samples[samples.length - 1].at) samples.push({ at, bytes })
      else samples[samples.length - 1].bytes = bytes
      while (samples.length > 2 && samples[1].at < at - 4000) samples.shift()
      const first = samples[0]
      const elapsed = at - first.at
      return elapsed >= 500 ? Math.max(0, (bytes - first.bytes) / elapsed * 1000) : 0
    },
  }
}

export function formatUploadSpeed(bps: number): string {
  if (!Number.isFinite(bps) || bps < 0) return '—'
  if (bps < 1024) return `${Math.round(bps)} B/s`
  if (bps < 1024 * 1024) return `${Math.round(bps / 1024)} KB/s`
  return `${(bps / 1024 / 1024).toFixed(1)} MB/s`
}

export function formatUploadEta(seconds: number | null): string {
  if (seconds === null || !Number.isFinite(seconds)) return '正在估算…'
  if (seconds < 1) return '即将完成'
  const rounded = Math.ceil(seconds)
  if (rounded < 60) return `预计剩余 ${rounded}秒`
  if (rounded < 3600) return `预计剩余 ${Math.ceil(rounded / 60)}分钟`
  return `预计剩余 ${Math.floor(rounded / 3600)}小时${Math.ceil(rounded % 3600 / 60)}分`
}

export function uploadProgressPercent(sent: number, total: number): number {
  return total > 0 ? Math.max(0, Math.min(100, Math.floor(sent / total * 100))) : 0
}
