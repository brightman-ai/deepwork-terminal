// What each character set of xterm.js headless 6.0.0 draws for the printable ASCII range, by running
// `ESC ( <flag>` and the characters 0x20..0x7e through it and reading the row back. Every designating
// flag worth trying is here, including ones xterm has no table for (those draw the characters as they are).
//
//   node dump_charsets.mjs <out.json>
//
// muxd/vt_charset_test.go compares the model's character sets with this file.
import { writeFileSync } from 'node:fs'
import { newTerminal } from './xterm_env.mjs'

const flags = [...'0AB45CRQKYE67ZH7=1238<UI%']
const ascii = Array.from({ length: 0x7f - 0x20 }, (_, i) => String.fromCharCode(0x20 + i)).join('')
const out = {}
for (const f of flags) {
  const term = newTerminal({ cols: 120, rows: 2, scrollback: 0 })
  await new Promise(r => term.write(new Uint8Array(Buffer.from(`\x1b(${f}${ascii}`, 'utf8')), r))
  const line = term.buffer.active.getLine(0)
  let s = ''
  for (let x = 0; x < ascii.length; x++) s += line.getCell(x).getChars() || ' '
  out[f] = s
  term.dispose()
}
writeFileSync(process.argv[2], JSON.stringify(out, null, 1) + '\n')
console.log('wrote', Object.keys(out).length, 'sets')
