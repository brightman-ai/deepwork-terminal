Generates `muxd/testdata/xterm_reflow_vectors.json`: random byte streams and resizes, each with the screen, cursor and scrollback that `@xterm/headless` 6.0.0 (the frontend's xterm.js) ends in, with `@xterm/addon-unicode11` loaded and selected (`unicode.activeVersion = '11'`), as the frontend runs it. Every terminal the diagnostics build comes from `xterm_env.mjs` (`newTerminal({ cols, rows })`), so the width tables are chosen in one place; `restore_dump.mjs` should use it too. `muxd/xterm_parity_test.go` replays them through the terminal model. Regenerate with `npm ci && node gen_vectors.mjs ../../../muxd/testdata/xterm_reflow_vectors.json resize 300` (add `noresize` to leave resizes out); the output must stay under about 1.5 MB.

`node gen_vectors.mjs <out.json> resize|noresize [count] [splits] [firstSeed]` — `splits` is the share of writes cut in two at a random byte (0.06), `firstSeed` where the seeds start (1), so a set further out than the committed one can be tried: `... noresize 1500 0.08 1000`. A failing vector is named after its seed (`seed17`, `seed17-alt`). Streams are written to xterm as bytes, the way the browser does it (`useTerminalChannel.ts` writes `new Uint8Array(data)`), so xterm's UTF-8 decoder is part of what is compared.

Besides the base mix (text, wide characters, motion, scrolling, erase, modes, the alternate screen), a vector carries kinds of input that a plain text stream never reaches:

- combining characters, joiners and variation selectors, stacked and not (after a control sequence, at the start of a row, at the margin);
- character sets: `ESC ( 0` and the other designations, SI/SO, the locking shifts, DECSC/DECRC, DECSTR/RIS;
- bytes that are not UTF-8, in every shape xterm's decoder tells apart (xterm drops them; it prints no U+FFFD);
- a control character inside an escape or control sequence (carried out), CAN/SUB/ESC (which abandon it), C1 controls;
- OSC/DCS/SOS/PM/APC strings, ended every way xterm ends them.

What the generator keeps out, because the model deliberately differs or xterm itself is inconsistent there:

- HTS/TBC (`ESC H`, `U+0088`): tab stops are not modelled;
- a cut sequence followed by a stray continuation byte, and a write cut inside a multi-byte character that has a `0x80` byte in it: xterm's decoder mishandles those (its defects, not the model's);
- streams that leave xterm itself inconsistent (the generator drops them and says how many): see the next paragraph.

Known and not ported: after a resize xterm (`BufferLine.copyCellsFrom`) copies the combining data of cells *beyond* the range it moves, so a cell can come out showing another cell's marks (a ZWJ family with one member twice, an "e" with the accents of a "u"). The model keeps each cell's own. The generator drops a vector when a resize leaves the marks, read in buffer order, as anything but a subsequence of what they were (a reflow may cut cells off, never duplicate or swap them); that is about 1 in 60 resize vectors now that emoji sequences are common in the text. Without the filter about 1 in 200 diverges.

Widths: `node dump_widths.mjs <out.json>` writes `muxd/testdata/xterm_unicode11_widths.json`, the width xterm's active table gives every printable code point; `TestRuneWidthMatchesXtermUnicode11` holds `runeWidth` to it. With this addon the emoji that have a presentation of their own are two columns (🤖 💰 🧠 ✨ ✅), the symbols agents also use are one (⏺ ✻ ⚠, with or without a variation selector after them), and a regional indicator is one.

`node dump_charsets.mjs <out.json>` writes `muxd/testdata/xterm_charsets.json`: what each character set draws for printable ASCII, by running them through xterm. `TestXtermInput_CharacterSetTablesMatchXterm` compares the model's tables with it.

`restore_dump.mjs` is the other half, for the keyframe serializer (`reconnect_pack.go`): it feeds each restore stream into a fresh headless xterm and, given the vector's own ops, replays those into a second one and compares every cell (character, width, colours, attributes, wrap flag). The root test `TestKeyframeRestoresWhatXtermWatchedThroughTheWholeStream` drives it and skips itself unless `XTERM_DIAG_DIR` points at this directory after `npm ci`:

    XTERM_DIAG_DIR=$PWD/scripts/diag/xtermvectors go test . -count=1 -run TestKeyframeRestoresWhatXterm -v

`XTERM_VECTORS=<file>` runs it over another vector set (any `gen_vectors.mjs` output; 3000 vectors take about six minutes).
