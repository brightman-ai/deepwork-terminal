# Terminal path corpus

Claude and Codex use the same production parser, pointer map and selection map.
The 2026-10-25 study froze 100 independent main transcripts from each CLI since
October 1, excluding child sessions, duplicate session IDs and the audit itself.
Its private manifest records source hashes and read-byte boundaries; later
transcript appends cannot change the snapshot.

`audit-terminal-path-contexts.py --out-dir <PRIVATE_DIRECTORY>` makes a broad
candidate pass with two surrounding visible lines. File-tool target fields and
patch file headers are independent expected identities. Prose, command text and
tool output remain candidates until reviewed. Reasoning, system/developer/user
prose, duplicated Codex events and file content payloads are excluded. Original
commands are never executed. `--manifest-in <manifest.json>` replays the frozen
snapshot and checks its hashes. Keep all extracted text private.

The study found 78,623 coarse candidates and 1,565 declared target occurrences;
894 distinct agent/operation/path identities represent read/edit/create work.
All 5,364 single-line checks passed before the fix. Of 21,456 simulated CLI
hard-wrap checks, 1,754 failed before the fix and zero fail after it. These are
formatter simulations, not claims that JSONL messages are terminal recordings.
Real xterm checks separately exercise Unicode 11, soft wrapping and reflow.

The permanent minimized fixture contains 96 declared targets and 48 reviewed
static prose references (79 Claude / 65 Codex). Name characters are masked while
preserving widths, path lengths, separators, punctuation and extensions. Source
hash IDs and record numbers map back to private context evidence. Literal spaces
and parentheses were absent from this study's authoritative fields, so their
additional tests are explicitly synthetic adversarial controls.

`transcriptPathCorpus.test.ts` combines the fixtures with eight CLI/prose record
families, four CLI widths and three xterm widths. Expected identities never come
from the production parser. `transcriptPathRegressions.test.ts` adds reviewed
failure mechanisms and independent-record counterexamples. Run `bun test` in
`frontend`, and `python3 -m unittest discover -s scripts/tests` at the repo root.
Bundle `terminal-transcript-corpus-harness.ts` for a private browser page with
`#terminal` and `#result` to run the same identities through the installed xterm.

Ownership precedes path lexing: Markdown/tool fields, final edit statistics,
fixed continuation gutters, then prose tokens. Preserve literal spaces in owned
fields. A receipt may use its immediately preceding Write target to disambiguate
a slash continuation. A bare field needs consistent printed-column geometry and
a prose terminator for that recovery. Separate roots without such evidence stay
separate. Dynamic shell expressions, URL targets and templates stay opaque.
Ambiguous prose without an identifiable filename boundary cannot be recovered
reliably from rendered text alone; do not invent an absolute path or delete
arbitrary whitespace to improve a score.
