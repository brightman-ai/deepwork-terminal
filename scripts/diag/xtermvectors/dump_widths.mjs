// The width xterm's active Unicode table gives every code point a terminal can print (U+00A0 ..
// U+10FFFF, surrogates left out), as runs: [first, last, width]. Written to
// muxd/testdata/xterm_unicode11_widths.json; TestRuneWidthMatchesXtermUnicode11 holds muxd's
// runeWidth to it for every code point.
//
//   node dump_widths.mjs <out.json>
import { writeFileSync } from 'node:fs'
import { newTerminal, UNICODE_VERSION } from './xterm_env.mjs'

const term = newTerminal({ cols: 10, rows: 2 })
const svc = term._core.unicodeService
if (svc.activeVersion !== UNICODE_VERSION) throw new Error(`active Unicode version is ${svc.activeVersion}`)
const runs = []
for (let cp = 0xa0; cp <= 0x10ffff; cp++) {
  if (cp >= 0xd800 && cp <= 0xdfff) continue
  const w = svc.wcwidth(cp)
  const last = runs[runs.length - 1]
  if (last && last[2] === w && last[1] === cp - 1) last[1] = cp
  else runs.push([cp, cp, w])
}
writeFileSync(process.argv[2], JSON.stringify({ version: UNICODE_VERSION, runs }) + '\n')
console.log('wrote', runs.length, 'runs for Unicode', UNICODE_VERSION)
