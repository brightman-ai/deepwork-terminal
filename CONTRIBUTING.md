# Public fixtures and evidence

This is a public repository. Write synthetic test inputs instead of copying
user messages, terminal captures, transcripts, private filenames or directory
names. This applies to source, comments, scripts, commit messages, screenshots,
CI output and artifacts. Removing a file in a later commit leaves its earlier
contents in Git history.

Reproduce the mechanism with invented examples: path prefixes, ASCII/CJK cell
widths, punctuation, literal spaces, source ranges, edit statistics, continuation
gutters, CLI hard wraps and xterm soft wraps/reflow. Keep expected identities
independent of the parser. A frozen synthetic row example plus generated layout
variations and negative controls is preferable to a real conversation excerpt.
Use generic roots such as `/work/sample-project` or `/home/tester/work/demo`.

Keep original investigations, audit manifests, source hashes, session IDs,
private screenshots and history backups outside Git, in an ignored private
directory. A source manifest or a hash linking back to a private transcript
does not belong in an exported fixture. Review diffs and test diagnostics before
publishing. Screenshots for documentation must use an isolated synthetic demo.

Run `python3 scripts/check-public-fixtures.py` before publishing fixture changes.
The guard catches common machine paths and provenance exports; it cannot judge
whether an arbitrary filename or prose fragment came from a user. Human/agent
review of every exported example remains required.
