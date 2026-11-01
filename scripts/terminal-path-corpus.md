# Synthetic terminal path corpus

Claude and Codex share the production path parser, pointer map and selection
map. All public examples use fabricated names and text. The 144 grammar cases
exercise path lengths, ASCII/CJK display widths, separators, punctuation,
extensions, tool operations and prose wrappers. The fixture is a synthetic
regression corpus, not a transcript dataset. No source hashes, session IDs,
source line numbers or private run locations belong in it.

`transcriptPathCorpus.test.ts` combines literal expected identities with eight
CLI/prose record families, four CLI widths and three xterm widths. Some width
fixtures use repeated `文` characters deliberately. Expected paths must be
independent of the production parser. `transcriptPathRegressions.test.ts` adds
specific failure mechanisms and independent-record counterexamples.

Run `bun test` in `frontend`, and `python3 -m unittest discover -s scripts/tests`
at the repo root. The browser harnesses exercise the installed xterm with
Unicode 11, physical cells, selection, soft wrapping and reflow. They simulate
CLI formatting; they do not claim to execute Claude or Codex.

For private investigation only, `audit-terminal-path-contexts.py --out-dir
<PRIVATE_DIRECTORY>` extracts coarse candidates and surrounding context from
local logs. Keep manifests, text, original paths and all resulting evidence
outside Git. Export a newly authored synthetic counterexample that reproduces
the grammar and display geometry. Never copy a private record into a public
test, comment, screenshot, commit message or CI diagnostic.

Ownership precedes path lexing: Markdown/tool fields, final edit statistics,
fixed continuation gutters, then prose tokens. Preserve literal spaces in owned
fields. A receipt may use its immediately preceding Write target to disambiguate
a slash continuation. A bare field needs consistent printed-column geometry and
a prose terminator for that recovery. Separate roots without such evidence stay
separate. Dynamic shell expressions, URL targets and templates stay opaque.
Ambiguous prose without an identifiable filename boundary cannot be recovered
reliably from rendered text alone; do not invent paths or remove arbitrary
whitespace to improve a score.
