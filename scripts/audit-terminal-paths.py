#!/usr/bin/env python3
"""Read local agent JSONL without exporting messages, commands, or credentials.

Usage: python3 scripts/audit-terminal-paths.py --out /private/path/report.json
Counts are path-like lexeme occurrences, not a measurement of rendered TUI output.
Profile roots and files are deduplicated by device/inode. Reasoning and duplicated
Codex event messages are excluded. Provenance distinguishes prose from tool data.
"""
import argparse
import collections
import hashlib
import json
from pathlib import Path
import re
import time
import unicodedata

TOKEN = re.compile(r'''[^\s<>"'`|\x00-\x1f]{1,1024}''')
QUOTED = re.compile(r'''(["'`])([^\r\n"'`]{1,1024})\1''')
FILE_END = re.compile(r"\.([A-Za-z][A-Za-z0-9]{0,15})(?::\d+(?::\d+|-\d+)?|#L\d+(?:-L\d+)?)?$")
KNOWN = {'Dockerfile', 'Makefile', 'LICENSE', 'README', '.gitignore', '.env', '.npmrc', 'AGENTS.md'}
EXTENSIONS = set('md markdown mdx txt log json jsonl yaml yml toml ini conf xml html htm css scss js ts tsx jsx mjs cjs vue py go rs rb swift m mm c h cpp cs sh zsh bash sql proto pdf doc docx ppt pptx xls xlsx csv tsv png jpg jpeg webp gif svg zip gz tar xmind srt vtt env lock plist'.split())


def roots():
    home = Path.home()
    choices = [('claude', home / '.claude/projects'), ('codex', home / '.codex/sessions'),
               ('codex', home / '.codex/archived_sessions')]
    for agent, suffix in [('claude', 'projects'), ('codex', 'sessions')]:
        profiles = home / ('.' + agent + '-profiles')
        if profiles.is_dir():
            choices.extend((agent, p / suffix) for p in sorted(profiles.iterdir()) if p.is_dir())
            if agent == 'codex':
                choices.extend((agent, p / 'archived_sessions') for p in sorted(profiles.iterdir()) if p.is_dir())
    seen = set()
    for agent, path in choices:
        try:
            stat = path.stat()
        except OSError:
            continue
        identity = stat.st_dev, stat.st_ino
        if identity not in seen:
            seen.add(identity)
            yield agent, path


def text_blocks(value, source):
    if isinstance(value, str):
        yield source, value
    elif isinstance(value, list):
        for child in value:
            yield from text_blocks(child, source)
    elif isinstance(value, dict):
        kind = value.get('type', '')
        if kind in ('tool_use', 'function_call', 'custom_tool_call'):
            # Actual tool file targets, not arbitrary shell code or serialized arguments.
            args = value.get('input', {})
            if isinstance(args, dict):
                for key in ('file_path', 'path', 'target_file', 'notebook_path'):
                    if isinstance(args.get(key), str):
                        yield 'tool-target', args[key]
        elif kind == 'tool_result':
            yield from text_blocks(value.get('content', []), 'tool-output')
        else:
            for key in ('text', 'content'):
                if key in value:
                    yield from text_blocks(value[key], source)


def record_text(agent, row):
    if agent == 'claude' and row.get('type') in ('assistant', 'user'):
        message = row.get('message', {})
        if isinstance(message, dict):
            yield from text_blocks(message.get('content', []), row['type'])
    elif agent == 'codex' and row.get('type') == 'response_item':
        value = row.get('payload', {})
        kind = value.get('type')
        if kind == 'message':
            yield from text_blocks(value.get('content', []), value.get('role', 'unknown'))
        elif kind in ('function_call_output', 'custom_tool_call_output'):
            yield from text_blocks(value.get('output', ''), 'tool-output')
        elif kind == 'function_call':
            try:
                args = json.loads(value.get('arguments', '{}'))
            except (ValueError, TypeError):
                return
            if isinstance(args, dict):
                for key in ('file_path', 'path', 'target_file', 'notebook_path'):
                    if isinstance(args.get(key), str):
                        yield 'tool-target', args[key]


def candidates(text):
    # Quote-bound paths include spaces; don't expose surrounding sentences.
    for match in QUOTED.finditer(text):
        value = match[2]
        extension = FILE_END.search(value)
        if (' ' in value and '/' in value and extension and extension[1].lower() in EXTENSIONS
                and value == value.strip() and ' / ' not in value
                and not re.search(r'\s', value.split('/', 1)[0])
                and not re.match(r'^(?:bash|sh|zsh|cat|curl|node|python3?|go|bun|npm|pnpm|ls|rg|sed|grep|git|dw-browser)\s', value)
                and not any(c in value for c in ('=', ' --', ' -', ';', '|', '$', '&&'))
                and not value.startswith('-')):
            yield value, True
    for match in TOKEN.finditer(text):
        value = match[0].strip('()[]{};,。')
        if '](' in value:
            value = value.split('](', 1)[1].strip('<>()')
        value = value.strip('*')
        if len(value) > 512 or any(c in value for c in ('\\n', '\\r', '\\t')) or '://' in value and not value.startswith('file://'):
            continue
        extension = FILE_END.search(value)
        if (extension and extension[1].lower() in EXTENSIONS) or value.rsplit('/', 1)[-1] in KNOWN:
            # Ignore code expressions and content disguised as filenames.
            if not any(c in value for c in ('=', '\x1b', '[', ']', '{', '}')):
                yield value, False


def features(value, quoted):
    result = ['path' if '/' in value else 'basename']
    if quoted:
        result.append('quoted-space')
    if value.startswith(('/', '~/')):
        result.append('absolute-or-home')
    if value.startswith(('./', '../', '@./', '@/')):
        result.append('explicit-relative-or-at')
    if '...' in value or '…' in value:
        result.append('ellipsis')
    if any('\u4e00' <= c <= '\u9fff' for c in value):
        result.append('cjk')
    if any(unicodedata.category(c).startswith('M') for c in value):
        result.append('combining-mark')
    if any(unicodedata.category(c) == 'So' for c in value):
        result.append('symbol-or-emoji')
    if '(' in value or ')' in value:
        result.append('parenthesis')
    if re.search(r':\d+:\d+$', value):
        result.append('ambiguous-line-column-range')
    elif re.search(r':\d+-\d+$|#L\d+', value):
        result.append('explicit-range')
    elif re.search(r':\d+$', value):
        result.append('line')
    if value.rsplit('/', 1)[-1] in KNOWN:
        result.append('known-special-name')
    if '/.' in value:
        result.append('dot-directory')
    if '\\' in value:
        result.append('backslash')
    if '*' in value or '$' in value:
        result.append('pattern-or-expression')
    if len(value) > 80:
        result.append('long-over-80-codepoints')
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--out', required=True, type=Path)
    parser.add_argument('--exclude-session', action='append', default=[], help='Exclude this study session from samples/counts')
    args = parser.parse_args()
    started = time.monotonic()
    stats, counts, sources = collections.Counter(), collections.Counter(), collections.Counter()
    samples, seen_files, sample_ids = {}, set(), set()
    for agent, root in roots():
        for path in sorted(root.rglob('*.jsonl')):
            if any(session in path.name for session in args.exclude_session):
                stats['excluded_study_sessions'] += 1
                continue
            try:
                stat = path.stat()
                identity = stat.st_dev, stat.st_ino
                if identity in seen_files:
                    continue
                seen_files.add(identity)
                stats[agent + '_files'] += 1
                stats['bytes'] += stat.st_size
                with path.open(errors='replace') as stream:
                    for line in stream:
                        stats['records'] += 1
                        try:
                            row = json.loads(line)
                        except ValueError:
                            stats['invalid_json_records'] += 1
                            continue
                        if not isinstance(row, dict):
                            continue
                        for source, text in record_text(agent, row):
                            stats['text_blocks'] += 1
                            for value, quoted in candidates(text):
                                sources[agent + ':' + source] += 1
                                for feature in features(value, quoted):
                                    key = agent + ':' + source + ':' + feature
                                    counts[key] += 1
                                    # Bounded, path-only samples. Do not export commands/messages.
                                    bucket = samples.setdefault(key, [])
                                    digest = hashlib.sha256(value.encode()).hexdigest()[:16]
                                    if len(bucket) < 5 and (key, digest) not in sample_ids:
                                        clean = value.replace(str(Path.home()), '<HOME>')
                                        if clean.startswith(('/', '<HOME>/')):
                                            clean = '<ABS>/…/' + '/'.join(clean.split('/')[-3:])
                                        clean = re.sub(r'(?i)(sk-|ghp_|github_pat_)[\w-]+', '<REDACTED>', clean)
                                        bucket.append({'path': clean, 'source_id': hashlib.sha256(str(path).encode()).hexdigest()[:12]})
                                        sample_ids.add((key, digest))
            except OSError:
                stats['unreadable_files'] += 1
        print(agent + ': scanned ' + str(len(seen_files)) + ' unique files', flush=True)
    report = {'schema': 1, 'script_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest(), 'limitations': [
        'Counts are path-like lexeme occurrences, including repeated and injected content.',
        'Assistant/user prose, tool targets and tool outputs are separated; reasoning/event duplicates excluded.',
        'Transcripts do not preserve TUI layout, OSC targets, output-time cwd or xterm wrapping.',
        'Samples are bounded path-only snippets; profiles sharing inode identities are scanned once.',
        'Default and profile roots only; unreadable/malformed records are reported.',
        'An extension allowlist bounds false positives; counts are not all possible filesystem paths.',
    ], 'excluded_sessions': args.exclude_session, 'elapsed_seconds': round(time.monotonic() - started, 2), 'scan': dict(stats),
        'sources': dict(sources), 'features': dict(counts), 'samples': samples}
    args.out.parent.mkdir(parents=True, exist_ok=True)
    args.out.write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps({'elapsed_seconds': report['elapsed_seconds'], 'scan': report['scan']}, ensure_ascii=False))


if __name__ == '__main__':
    main()
