// gen-pwa-icons.mjs — deterministic brand-icon generator (no native deps).
//
// Rasterises the Deepwork-Terminal mark — an amber `>_` shell prompt on a dark
// rounded tile — into the PNGs the PWA manifest + Apple meta tags reference. Pure
// Node (zlib only) so it runs anywhere the repo builds; re-run with `node
// scripts/gen-pwa-icons.mjs` if the brand mark changes. Output lands in public/.
import { deflateSync } from 'node:zlib'
import { writeFileSync, mkdirSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const OUT = join(dirname(fileURLToPath(import.meta.url)), '..', 'public')
mkdirSync(OUT, { recursive: true })

// v6 paradigm palette (oklch amber accent ≈ #f08a3c; surface ≈ #141416 / #0d0d0f).
const BG = [13, 13, 15] // --bg
const TILE = [20, 20, 22] // --sf
const ACCENT = [240, 138, 60] // --ac amber
const MUTED = [120, 120, 128]

function crc32(buf) {
  let c = ~0
  for (let i = 0; i < buf.length; i++) {
    c ^= buf[i]
    for (let k = 0; k < 8; k++) c = (c >>> 1) ^ (0xedb88320 & -(c & 1))
  }
  return ~c >>> 0
}
function chunk(type, data) {
  const len = Buffer.alloc(4); len.writeUInt32BE(data.length, 0)
  const t = Buffer.from(type, 'ascii')
  const body = Buffer.concat([t, data])
  const crc = Buffer.alloc(4); crc.writeUInt32BE(crc32(body), 0)
  return Buffer.concat([len, body, crc])
}
function encodePNG(size, rgba) {
  const sig = Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a])
  const ihdr = Buffer.alloc(13)
  ihdr.writeUInt32BE(size, 0); ihdr.writeUInt32BE(size, 4)
  ihdr[8] = 8; ihdr[9] = 6 // 8-bit, RGBA
  const stride = size * 4
  const raw = Buffer.alloc((stride + 1) * size)
  for (let y = 0; y < size; y++) {
    raw[y * (stride + 1)] = 0 // filter: none
    rgba.copy(raw, y * (stride + 1) + 1, y * stride, y * stride + stride)
  }
  return Buffer.concat([
    sig,
    chunk('IHDR', ihdr),
    chunk('IDAT', deflateSync(raw, { level: 9 })),
    chunk('IEND', Buffer.alloc(0)),
  ])
}

// Tiny 5x7 bitmap font for the `>_` mark is overkill; we draw geometry directly.
function draw(size, { maskable }) {
  const buf = Buffer.alloc(size * size * 4)
  const px = (x, y, [r, g, b], a = 255) => {
    if (x < 0 || y < 0 || x >= size || y >= size) return
    const i = (y * size + x) * 4
    buf[i] = r; buf[i + 1] = g; buf[i + 2] = b; buf[i + 3] = a
  }
  // Maskable icons must keep content inside the ~80% safe zone (40% radius).
  const inset = maskable ? Math.round(size * 0.10) : 0
  const radius = maskable ? 0 : Math.round(size * 0.22)
  for (let y = 0; y < size; y++) {
    for (let x = 0; x < size; x++) {
      px(x, y, BG) // full-bleed bg (covers maskable overscan)
    }
  }
  // Rounded tile
  const t0 = inset, t1 = size - inset
  for (let y = t0; y < t1; y++) {
    for (let x = t0; x < t1; x++) {
      if (radius > 0) {
        const cx = x < t0 + radius ? t0 + radius : x > t1 - radius ? t1 - radius : x
        const cy = y < t0 + radius ? t0 + radius : y > t1 - radius ? t1 - radius : y
        if ((x - cx) ** 2 + (y - cy) ** 2 > radius ** 2) continue
      }
      px(x, y, TILE)
    }
  }
  // Geometry of the `>` chevron + `_` underscore, scaled to tile.
  const u = (size - 2 * inset) / 32 // unit
  const ox = inset, oy = inset
  const thick = Math.max(2, Math.round(u * 2.4))
  // Chevron `>`: two strokes from (8,9)->(15,16)->(8,23)
  const seg = (x0, y0, x1, y1, col) => {
    const steps = Math.max(Math.abs(x1 - x0), Math.abs(y1 - y0)) * 4 + 1
    for (let s = 0; s <= steps; s++) {
      const fx = x0 + (x1 - x0) * (s / steps)
      const fy = y0 + (y1 - y0) * (s / steps)
      for (let dy = -thick / 2; dy <= thick / 2; dy++)
        for (let dx = -thick / 2; dx <= thick / 2; dx++)
          px(Math.round(ox + fx * u + dx), Math.round(oy + fy * u + dy), col)
    }
  }
  seg(9, 9, 16, 16, ACCENT)
  seg(16, 16, 9, 23, ACCENT)
  // Underscore `_`: horizontal bar (18,22)->(25,22)
  seg(18, 22, 25, 22, MUTED)
  return buf
}

const targets = [
  { name: 'pwa-192.png', size: 192, maskable: false },
  { name: 'pwa-512.png', size: 512, maskable: false },
  { name: 'pwa-maskable-512.png', size: 512, maskable: true },
  { name: 'apple-touch-icon.png', size: 180, maskable: false },
  { name: 'favicon-32.png', size: 32, maskable: false },
]
for (const t of targets) {
  writeFileSync(join(OUT, t.name), encodePNG(t.size, draw(t.size, t)))
  console.log('wrote', t.name, `${t.size}x${t.size}`)
}
