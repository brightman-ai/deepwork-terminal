#!/usr/bin/env python3
"""Freeze a balanced transcript sample and extract private path/context evidence.

Targets declared by file tools / apply_patch are independent of the terminal
parser. Prose hits are COARSE candidates, never automatically accepted labels.
Only minimized, reviewed examples belong in a public regression fixture.
"""
import argparse
import collections
import datetime as dt
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import random
import re

spec = importlib.util.spec_from_file_location('path_lexeme_audit', Path(__file__).with_name('audit-terminal-paths.py'))
lexemes = importlib.util.module_from_spec(spec)
spec.loader.exec_module(lexemes)

OPS = {'read': 'read', 'write': 'create', 'edit': 'edit', 'multiedit': 'edit', 'notebookedit': 'edit'}
PATCH = re.compile(r'^\*\*\* (Add File|Update File|Delete File|Move to): (.+)$')
UUID = re.compile(r'[0-9a-f]{8}-[0-9a-f-]{27,}', re.I)
SECRET = re.compile(r'(?i)(?<![\w-])(?:sk-[\w-]{12,}|ghp_[\w]{16,}|github_pat_[\w]{16,}|Bearer\s+[\w.+/-]{12,})')
ASSIGNMENT = re.compile(r'''(?i)((?:api[_-]?key|access[_-]?token|authorization|password|auth-code)\s*(?:[=:]|\s)\s*["']?)[^\s"',;]+''')
SIGNED_QUERY = re.compile(r'(?i)((?:[?&]|&amp;)(?:signature|ucloudpublickey|x-amz-signature|x-amz-credential|access_token|token|api_key)=)[^&\s"\'<>]+')
EXTENSION_LOCATOR = re.compile(r'\.(?:' + '|'.join(sorted(lexemes.EXTENSIONS, key=len, reverse=True)) + r')(?=$|[^A-Za-z0-9_-])', re.I)


def coarse_candidates(line):
    yield from lexemes.candidates(line)
    # Suffix-only lexing misses exactly the problematic cases: a known extension
    # followed by a Chinese note, Markdown decoration, or an edit-stat suffix.
    # Keep their whole lexical container for model review, not as an expected path.
    for match in EXTENSION_LOCATOR.finditer(line):
        start, end = match.start(), match.end()
        while start > 0 and match.start() - start < 2048 and line[start - 1] not in ' \t"\'`|<>':
            start -= 1
        while end < len(line) and end - match.end() < 2048 and line[end] not in ' \t"\'`|<>':
            end += 1
        yield line[start:end], False


def scrub(text):
    return SIGNED_QUERY.sub(r'\1<REDACTED>', ASSIGNMENT.sub(r'\1<REDACTED>', SECRET.sub('<REDACTED>', text)))


def context(lines, at, needle):
    out = []
    for i in range(max(0, at - 2), min(len(lines), at + 3)):
        text = lines[i]
        if len(text) > 1000:
            pos = text.find(needle) if i == at else 0
            start = max(0, pos - 240)
            text = ('…' if start else '') + text[start:start + max(1000, len(needle) + 240)] + '…'
        out.append({'line': i + 1, 'text': scrub(text)})
    return out


def patch_targets(text):
    lines = text.splitlines()
    for i, line in enumerate(lines):
        match = PATCH.match(line)
        if match:
            operation = {'Add File': 'create', 'Update File': 'edit', 'Delete File': 'delete', 'Move to': 'move'}[match[1]]
            yield match[2], operation, context(lines, i, match[2])


def visible_blocks(agent, row):
    """Yield public text, declared targets, or patch input. No reasoning traversal."""
    if agent == 'claude':
        if row.get('type') not in ('assistant', 'user'):
            return
        role = row['type']
        message = row.get('message', {})
        content = message.get('content', []) if isinstance(message, dict) else []
        if isinstance(content, str):
            if role == 'assistant':
                yield {'source': 'assistant', 'text': content}
            return
        for item in content if isinstance(content, list) else []:
            if not isinstance(item, dict):
                continue
            kind = item.get('type')
            if kind == 'text' and role == 'assistant':
                yield {'source': 'assistant', 'text': item.get('text', '')}
            elif kind == 'tool_use':
                name = item.get('name', '')
                args = item.get('input', {})
                if not isinstance(args, dict):
                    continue
                for key in ('file_path', 'path', 'target_file', 'notebook_path'):
                    if isinstance(args.get(key), str):
                        yield {'source': 'tool-target', 'path': args[key], 'tool': name,
                               'operation': OPS.get(name.lower(), 'other'),
                               'offset': args.get('offset'), 'limit': args.get('limit')}
                if name.lower() == 'bash' and isinstance(args.get('command'), str):
                    yield {'source': 'tool-command', 'text': args['command'], 'tool': name}
            elif kind == 'tool_result':
                result = item.get('content', [])
                if isinstance(result, str):
                    yield {'source': 'tool-output', 'text': result}
                elif isinstance(result, list):
                    for block in result:
                        if isinstance(block, dict) and block.get('type') == 'text':
                            yield {'source': 'tool-output', 'text': block.get('text', '')}
    elif row.get('type') == 'response_item':
        payload = row.get('payload', {})
        if not isinstance(payload, dict):
            return
        kind = payload.get('type')
        if kind == 'message' and payload.get('role') == 'assistant':
            for block in payload.get('content', []):
                if isinstance(block, dict) and block.get('type') in ('text', 'output_text'):
                    yield {'source': 'assistant', 'text': block.get('text', '')}
        elif kind in ('function_call', 'custom_tool_call'):
            name = payload.get('name', '')
            raw = payload.get('input', payload.get('arguments', ''))
            if 'apply_patch' in name and isinstance(raw, str) and '*** ' in raw:
                yield {'source': 'patch-input', 'text': raw, 'tool': name}
            try:
                args = raw if isinstance(raw, dict) else json.loads(raw)
            except (ValueError, TypeError):
                args = None
                if isinstance(raw, str) and name in ('exec', 'functions.exec'):
                    yield {'source': 'tool-command', 'text': raw, 'tool': name}
            if isinstance(args, dict):
                for key in ('file_path', 'path', 'target_file', 'notebook_path'):
                    if isinstance(args.get(key), str):
                        yield {'source': 'tool-target', 'path': args[key], 'tool': name,
                               'operation': OPS.get(name.lower(), 'other')}
                for key in ('patch', 'input'):
                    if 'apply_patch' in name and isinstance(args.get(key), str):
                        yield {'source': 'patch-input', 'text': args[key], 'tool': name}
                for key in ('cmd', 'command'):
                    if isinstance(args.get(key), str):
                        yield {'source': 'tool-command', 'text': args[key], 'tool': name}
        elif kind in ('function_call_output', 'custom_tool_call_output') and isinstance(payload.get('output'), str):
            yield {'source': 'tool-output', 'text': payload['output']}


def inventory(since, excludes):
    seen, sessions = set(), set()
    out, skipped = [], collections.Counter()
    for agent, root in lexemes.roots():
        for path in root.rglob('*.jsonl'):
            if 'subagents' in path.parts:
                skipped['subagents'] += 1
                continue
            if agent == 'claude' and not UUID.fullmatch(path.stem):
                skipped['non_main_transcript'] += 1
                continue
            if any(value and value in path.name for value in excludes):
                skipped['study_session'] += 1
                continue
            stat = path.stat()
            if stat.st_mtime < since:
                continue
            identity = stat.st_dev, stat.st_ino
            match = UUID.search(path.name)
            session = (agent, match[0] if match else path.stem)
            if identity in seen or session in sessions:
                skipped['duplicate_session_or_inode'] += 1
                continue
            seen.add(identity)
            sessions.add(session)
            project = path.parent.name if agent == 'claude' else ''
            if agent == 'codex':
                try:
                    with path.open('rb') as stream:
                        metadata = json.loads(stream.readline(256_000)).get('payload', {})
                    source = metadata.get('source', {})
                    if isinstance(source, dict) and 'subagent' in source:
                        skipped['codex_subagent'] += 1
                        continue
                    project = metadata.get('cwd', '')
                except (ValueError, OSError, AttributeError):
                    skipped['unknown_codex_metadata'] += 1
            out.append({'agent': agent, 'path': str(path), 'project': project,
                        'source_id': hashlib.sha256(str(path).encode()).hexdigest()[:16],
                        'frozen_bytes': stat.st_size, 'mtime_ns': stat.st_mtime_ns,
                        'day': dt.datetime.fromtimestamp(stat.st_mtime, dt.timezone.utc).strftime('%Y-%m-%d')})
    return out, skipped


def select(files, per_agent, seed):
    chosen = []
    for agent in ('claude', 'codex'):
        buckets = collections.defaultdict(list)
        for entry in files:
            if entry['agent'] == agent:
                buckets[(entry['day'], entry['project'])].append(entry)
        rng = random.Random(f'{seed}:{agent}')
        for bucket in buckets.values():
            bucket.sort(key=lambda x: x['path'])
            rng.shuffle(bucket)
        keys = sorted(buckets)
        rng.shuffle(keys)
        picked = []
        while len(picked) < per_agent and any(buckets.values()):
            for key in keys:
                if buckets[key]:
                    picked.append(buckets[key].pop())
                    if len(picked) == per_agent:
                        break
        if len(picked) != per_agent:
            raise ValueError(f'{agent}: only {len(picked)} independent transcripts, need {per_agent}')
        chosen.extend(picked)
    return chosen


def extract(selected, directory):
    counts, feature_counts = collections.Counter(), collections.Counter()
    targets = (directory / 'authoritative.jsonl').open('w')
    coarse = (directory / 'candidates.jsonl').open('w')
    for entry in selected:
        digest = hashlib.sha256()
        with Path(entry['path']).open('rb') as stream:
            number, consumed, hits = 0, 0, 0
            while consumed < entry['frozen_bytes']:
                raw = stream.readline(entry['frozen_bytes'] - consumed)
                if not raw:
                    break
                number += 1
                consumed += len(raw)
                digest.update(raw)
                if len(raw) > 16_000_000:
                    counts['oversized_records_skipped'] += 1
                    continue
                try:
                    row = json.loads(raw)
                except (ValueError, UnicodeDecodeError):
                    counts['invalid_or_incomplete_records'] += 1
                    continue
                if not isinstance(row, dict):
                    continue
                counts['records'] += 1
                for block in visible_blocks(entry['agent'], row):
                    base = {'agent': entry['agent'], 'source_id': entry['source_id'],
                            'jsonl_line': number, 'source': block['source'], 'tool': block.get('tool')}
                    if 'path' in block:
                        path = block['path']
                        value = base | {'path': path, 'operation': block['operation'],
                                        'offset': block.get('offset'), 'limit': block.get('limit'),
                                        'context': [{'line': 1, 'text': f'tool: {block.get("tool")}'},
                                                    {'line': 2, 'text': 'file_path: ' + scrub(path)}],
                                        'authority': 'declared-file-tool-target'}
                        targets.write(json.dumps(value, ensure_ascii=False) + '\n')
                        counts[entry['agent'] + ':authoritative:' + block['operation']] += 1
                        hits += 1
                        continue
                    text = block.get('text', '')
                    if block['source'] == 'patch-input':
                        for path, operation, around in patch_targets(text):
                            value = base | {'path': path, 'operation': operation, 'context': around,
                                            'authority': 'apply-patch-file-header'}
                            targets.write(json.dumps(value, ensure_ascii=False) + '\n')
                            counts[entry['agent'] + ':authoritative:' + operation] += 1
                            hits += 1
                    # The broad pass preserves neighbouring visible lines; labels,
                    # wrappers and command syntax are deliberately not accepted here.
                    lines = text.splitlines()
                    for index, line in enumerate(lines):
                        if len(line) > 100_000:
                            counts['oversized_visible_lines_skipped'] += 1
                            continue
                        found = set()
                        for value, quoted in coarse_candidates(line):
                            if value in found:
                                continue
                            found.add(value)
                            feats = lexemes.features(value, quoted)
                            if re.search(r'[:：]', line): feats.append('adjacent-colon-context')
                            if re.search(r'\(\s*\+\d+|\(\s*-\d+', line): feats.append('edit-stat-context')
                            if re.search(r'[（(].+[）)]', line): feats.append('parenthetical-context')
                            if re.search(r'Edited|Added|Write\(|Update\(|\*\*\* (?:Add|Update) File', line): feats.append('file-operation-context')
                            for feature in feats:
                                feature_counts[entry['agent'] + ':' + feature] += 1
                            hit = base | {'candidate': scrub(value), 'text_line': index + 1,
                                          'features': feats, 'context': context(lines, index, value),
                                          'authority': 'unreviewed-coarse-candidate'}
                            coarse.write(json.dumps(hit, ensure_ascii=False) + '\n')
                            counts[entry['agent'] + ':' + block['source'] + ':candidate'] += 1
                            hits += 1
            checksum = digest.hexdigest()
            if entry.get('read_sha256') and entry['read_sha256'] != checksum:
                raise ValueError(f"frozen prefix changed: {entry['source_id']}")
            entry.update(read_bytes=consumed, read_sha256=checksum, records=number, extracted_hits=hits)
        print(json.dumps({'agent': entry['agent'], 'source_id': entry['source_id'], 'hits': hits}), flush=True)
    targets.close()
    coarse.close()
    return counts, feature_counts


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--out-dir', required=True, type=Path)
    parser.add_argument('--per-agent', default=100, type=int)
    parser.add_argument('--since', default='2026-10-01')
    parser.add_argument('--seed', default='20261025')
    parser.add_argument('--exclude-session', action='append', default=[])
    parser.add_argument('--manifest-in', type=Path, help='Replay the exact frozen files and byte boundaries instead of resampling')
    args = parser.parse_args()
    args.out_dir.mkdir(parents=True, exist_ok=True)
    excludes = [*args.exclude_session, os.environ.get('CODEX_THREAD_ID', '')]
    if args.manifest_in:
        previous = json.loads(args.manifest_in.read_text())
        selected, skipped = previous['sources'], previous['skipped']
    else:
        files, skipped = inventory(dt.datetime.fromisoformat(args.since).replace(tzinfo=dt.timezone.utc).timestamp(), excludes)
        selected = select(files, args.per_agent, args.seed)
    counts, features = extract(selected, args.out_dir)
    manifest = {'schema': 1, 'seed': args.seed, 'since_mtime': args.since,
                'sample_size': len(selected), 'per_agent': args.per_agent,
                'excluded_sessions': [v for v in excludes if v], 'skipped': dict(skipped),
                'script_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest(), 'sources': selected}
    manifest['lexeme_script_sha256'] = hashlib.sha256(Path(__file__).with_name('audit-terminal-paths.py').read_bytes()).hexdigest()
    (args.out_dir / 'manifest.json').write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + '\n')
    summary = {'sample_size': len(selected), 'counts': dict(counts), 'features': dict(features),
               'limitations': ['Prose/command/output hits are unreviewed candidates, not labels.',
                               'JSONL text is not captured terminal layout; CLI rendering is a separate test stage.',
                               'Private contexts are bounded to two neighbouring lines with long-line clipping and credential redaction.',
                               'Reasoning/system/developer/user prose and duplicated event messages are excluded.',
                               'Sampling is stratified round-robin by modification day and Claude project; full prefix reads are frozen at inventory size.']}
    (args.out_dir / 'summary.json').write_text(json.dumps(summary, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps({'sample_size': len(selected), 'counts': dict(counts)}, ensure_ascii=False))


if __name__ == '__main__':
    main()
