import { describe, expect, it } from 'bun:test'
import { gitStatusWorkingDirectory } from '../terminalCommandContext'

describe('explicit Git short-status directory context',()=>{
 it('handles separators, quoted directories and CLI cuts through words or removed spaces',()=>{
  expect(gitStatusWorkingDirectory(['cd /work/current && git -C /work/other status --short docs | head'])).toBe('/work/other')
  expect(gitStatusWorkingDirectory(['git','-C /work/oth','er status --short','docs | head'])).toBe('/work/other')
  expect(gitStatusWorkingDirectory(['git -C "/work/other pro','ject" status -s'])).toBe('/work/other project')
  expect(gitStatusWorkingDirectory(["git -C '/work/other project' status --short"])).toBe('/work/other project')
  expect(gitStatusWorkingDirectory(['echo "📄 # note"; git -C /work/other status --short'])).toBe('/work/other')
 })
 it('ignores quoted examples and heredoc contents while retaining a later actual command',()=>{
  expect(gitStatusWorkingDirectory(['echo "git -C /work/wrong status --short"; git -C /work/right status --short'])).toBe('/work/right')
  expect(gitStatusWorkingDirectory(["F='/work/note.md'; python3 - \"$F\" <<'EOF'",'print("git -C /work/wrong status --short")','EOF','cd /work/current && echo ready; git','-C /work/right status --short','docs | head'])).toBe('/work/right')
  expect(gitStatusWorkingDirectory(["cat <<'EOF'",'git -C /work/wrong status --short','EOF'])).toBeUndefined()
 })
 it('does not invent roots for ambiguous, dynamic, relative, commented or unsupported commands',()=>{
  for(const text of [
   'git -C /work/one status --short; git -C /work/two status --short',
   'git -C "$ROOT" status --short', 'git -C ../other status --short',
   'git -C /work/other status --porcelain', 'git status --short',
   'echo $(git -C /work/wrong status --short)',
   'echo "git -C /work/wrong status --short"',
   'echo example\\;git -C /work/wrong status --short',
   '# git -C /work/wrong status --short',
   'git -C "$ROOT" status --short; git -C /work/other status --short',
  ])expect(gitStatusWorkingDirectory([text])).toBeUndefined()
 })
})
