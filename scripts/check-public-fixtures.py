#!/usr/bin/env python3
"""Conservative checks for public fixture exports; diagnostics omit content."""
import json
from pathlib import Path
import re
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
GENERIC_USERS = {'a', 'u', 'user', 'tester', 'demo', 'example', 'me', 'foo', 'someone', 'work'}
HOME = re.compile(r'/(?:home|Users)/([A-Za-z0-9_-]+)(?=[/\\\s\x27\x22`]|$)')
PROVENANCE = re.compile(r'[\x22\x27](?:private_run|source_id|jsonl_line)[\x22\x27]\s*:')
INLINE_AUTH = re.compile(r'https?://[^\s\x22\x27]*[?&](?:auth|token|api_key)=[A-Za-z0-9_-]{6,}')


def violations(text):
    found = []
    if any(m[1] not in GENERIC_USERS for m in HOME.finditer(text)):
        found.append('non-generic home directory')
    if PROVENANCE.search(text):
        found.append('private source provenance')
    if INLINE_AUTH.search(text):
        found.append('inline authentication value')
    return found


def fixture_path(path):
    return (path.endswith(('.test.ts', '_test.go')) or '/__tests__/' in path
            or '/testdata/' in path or (path.startswith('scripts/') and
                ('harness' in path or path.startswith('scripts/ux-'))))


def main():
    paths = subprocess.check_output(['git', 'ls-files', '-z'], cwd=ROOT).decode().split('\0')
    failures = []
    for name in filter(fixture_path, paths):
        path = ROOT / name
        if not path.is_file():
            continue
        try:
            text = path.read_text()
        except UnicodeDecodeError:
            failures.append((name, ['binary fixture needs synthetic-demo review']))
            continue
        reasons = violations(text)
        if reasons:
            failures.append((name, reasons))
    corpus = json.loads((ROOT / 'frontend/src/components/terminal-session/__tests__/fixtures/transcriptPathCorpus.json').read_text())
    if corpus['study'].get('kind') != 'synthetic-grammar-fixtures' or any('origin' in c for c in corpus['cases']):
        failures.append(('transcriptPathCorpus.json', ['synthetic fixture contract']))
    for name, reasons in failures:
        print(f'{name}: {", ".join(reasons)}', file=sys.stderr)
    print(f'Public fixture guard: {len(failures)} violation(s)')
    return bool(failures)


if __name__ == '__main__':
    sys.exit(main())
