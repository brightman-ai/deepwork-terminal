// Round-trip oracle for the keyframe serializer: feed each restore stream into a fresh headless
// xterm (the frontend's version) and dump the state the same way gen_vectors.mjs dumps its
// expectations. Usage: node restore_dump.mjs <in.json> <out.json>
//   in:  [{name, cols, rows, b64, start?, ops?}]   cols/rows are the size at the end
//   out: {vectors: [{name, expect: {alt, cursor, screen, normalScreen, scrollback}, cellDiff?}], skips: {reason: count}}
//
// With `start` (the first [cols, rows]) and `ops` (the writes, base64, and resizes, as
// gen_vectors.mjs records them), the stream that produced the state is replayed into a second
// terminal and every cell of the two is compared: character (a typed blank or a never-written
// cell), width, colours and attributes. cellDiff is the first difference, '' when there is none.
// The row text and wrap flag in `expect` can't tell a typed space from an empty cell, and see no
// style at all; the cells can.
import { readFileSync, writeFileSync } from 'node:fs'
import { newTerminal } from './xterm_env.mjs' // the terminal the vectors were made on: headless xterm with Unicode 11 widths, as the frontend runs it

const input = JSON.parse(readFileSync(process.argv[2], 'utf8'))
const out = []
const skips = {}

const newTerm = (cols, rows) => newTerminal({ cols, rows })
const feed = (term, data) => new Promise(res => term.write(data, res))

const markedCell = c => !c.isFgDefault() || c.isBold() || c.isItalic() || c.isDim() || c.isUnderline() || c.isBlink() ||
  c.isInverse() || c.isInvisible() || c.isStrikethrough() || c.isOverline()

// A palette colour is the same colour whether SGR 31 or 38;5;1 set it (xterm tells them apart by
// mode, but draws them alike), and the model keeps only the index.
const colour = (rgb, palette, v) => rgb ? `rgb:${v}` : palette ? `pal:${v}` : 'default'
const styleSig = c => sig(c).split('|').slice(2).join('|')
const sig = c => [
  c.getChars(), c.getWidth(),
  colour(c.isFgRGB(), c.isFgPalette(), c.getFgColor()), colour(c.isBgRGB(), c.isBgPalette(), c.getBgColor()),
  +c.isBold(), +c.isItalic(), +c.isDim(), +c.isUnderline(), +c.isBlink(),
  +c.isInverse(), +c.isInvisible(), +c.isStrikethrough(), +c.isOverline(),
].join('|')

// The first cell inside the grid where the two buffers differ. The normal buffer is compared with
// its scrollback; the alternate one by its screen alone (it keeps no history, and xterm leaves it
// as many lines as it was ever tall).
//
// Cells no output can leave in a terminal of this width are not compared, and counted in `skips` by
// reason: they exist only because a resize made them.
//   - a double-width character with its first half in the last column;
//   - a cell beside a double-width character that is not the second half of it, anywhere but in the
//     last column (autowrap off leaves one there; a resize carries it into the middle of a row);
//   - a second-half cell with no double-width character before it;
// and one thing the restore cannot do: an empty last cell that holds more than a background, in a
// row that a continuation follows (the wrap needs that cell written, and erasing keeps the
// background only) or without a character of that style before it (the only way to leave one).
function firstCellDiff(what, got, want, cols, rows, skips) {
  const from = what === 'alternate' ? want.baseY : 0
  if (got.baseY !== want.baseY && what !== 'alternate') return `${what}: ${got.baseY} lines of scrollback, want ${want.baseY}`
  const a = got.getNullCell(), b = want.getNullCell(), c = want.getNullCell()
  const skip = reason => { skips[reason] = (skips[reason] || 0) + 1 }
  for (let i = from; i < want.baseY + rows; i++) {
    const lg = got.getLine(i - want.baseY + got.baseY), lw = want.getLine(i)
    if (lg.isWrapped !== lw.isWrapped) return `${what} row ${i}: wrapped=${lg.isWrapped}, want ${lw.isWrapped}`
    const next = i + 1 < want.baseY + rows ? want.getLine(i + 1) : null
    for (let x = 0; x < cols; x++) {
      const cw = lw.getCell(x, b)
      if (x === cols - 1 && cw.getWidth() === 2) { skip('wide-first-half-in-last-column'); continue }
      if (cw.getWidth() === 0 && cw.getChars() === '' && (x === 0 || lw.getCell(x - 1, c).getWidth() !== 2)) {
        // a second-half cell with no wide character in front of it (deleting or inserting characters
        // next to a wide character leaves one); the model keeps no record of it
        skip('orphan-second-half-cell'); continue
      }
      if (cw.getWidth() === 2 && x + 1 < cols - 1 && lw.getCell(x + 1, c).getWidth() !== 0) {
        // (and the second half of the cell beside it, if that is itself a wide character)
        skip('cell-beside-wide-character-mid-row'); x += lw.getCell(x + 1, c).getWidth() === 2 ? 2 : 1; continue
      }
      if (x === cols - 1 && cw.getWidth() === 1 && cw.getChars() === '' && markedCell(cw)) {
        if (next && next.isWrapped) {
          const f = next.getCell(0, c) // (a wide character that wrapped early leaves the cell in its own style)
          if (f.getWidth() !== 2 || styleSig(f) !== styleSig(cw)) { skip('marked-empty-last-cell-before-continuation'); continue }
        } else {
          let q = x - 1
          if (q > 0 && lw.getCell(q, c).getWidth() === 0) q--
          const pc = q >= 0 ? lw.getCell(q, c) : null
          if (!pc || pc.getChars() === '' || styleSig(pc) !== styleSig(cw)) { skip('marked-empty-last-cell-without-a-character-of-its-style-before-it'); continue }
        }
      }
      const sg = sig(lg.getCell(x, a)), sw = sig(cw)
      if (sg !== sw) return `${what} row ${i} col ${x}: cell ${JSON.stringify(sg)}, want ${JSON.stringify(sw)}`
    }
  }
  return ''
}

for (const v of input) {
  const term = newTerm(v.cols, v.rows)
  await feed(term, Buffer.from(v.b64, 'base64'))
  const dump = (buf, from, to) => { const o = []; for (let i = from; i < to; i++) { const l = buf.getLine(i); o.push({ t: l ? l.translateToString(true) : '', w: l ? l.isWrapped : false }) } return o }
  const a = term.buffer.active, nb = term.buffer.normal
  const rec = { name: v.name, expect: {
    alt: a.type === 'alternate',
    cursor: [a.cursorX, a.cursorY],
    screen: dump(a, a.baseY, a.baseY + v.rows),
    normalScreen: dump(nb, nb.baseY, nb.baseY + v.rows),
    scrollback: dump(nb, 0, nb.baseY),
  } }
  if (v.ops) {
    const ref = newTerm(v.start[0], v.start[1])
    for (const op of v.ops) {
      if (op.resize) ref.resize(op.resize[0], op.resize[1])
      else await feed(ref, Buffer.from(op.w, 'base64'))
    }
    await feed(ref, '')
    const r = ref.buffer.active
    rec.cellDiff = r.type !== a.type ? `active buffer ${a.type}, want ${r.type}` :
      firstCellDiff(a.type, a, r, v.cols, v.rows, skips) ||
      (r.type === 'alternate' ? firstCellDiff('parked normal', nb, ref.buffer.normal, v.cols, v.rows, skips) : '')
    ref.dispose()
  }
  out.push(rec)
  term.dispose()
}
writeFileSync(process.argv[3], JSON.stringify({ vectors: out, skips }))
