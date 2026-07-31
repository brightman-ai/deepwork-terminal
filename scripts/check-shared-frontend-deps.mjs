#!/usr/bin/env node
// check-shared-frontend-deps — the executable contract for consumers of this repo's frontend.
//
// ── Why this exists ──────────────────────────────────────────────────────────────────────────
// deepwork-terminal/frontend/src is not just this app's UI: deepwork-pro consumes it directly
// through a Vite alias (`@terminal` → ../../deepwork-terminal/frontend/src) rather than as a
// published package. Source is shared; node_modules is NOT. So the moment a component here adds
// an import, pro's build breaks — and it breaks with a module-resolution error pointing at a file
// in ANOTHER repo, which is a genuinely confusing place to land.
//
// That is not hypothetical. `@xterm/addon-search` arrived with in-terminal search on 2026-07-26
// and pro was never told; pro's embedded SPA has been stuck on its 2026-07-21 build ever since,
// silently serving a UI ten days behind while every rebuild attempt would have failed. `qrcode`
// had drifted the same way.
//
// The fix is not a second hand-maintained list — that is the same failure with more steps. The
// requirement is DERIVED from the source: whatever `frontend/src` imports IS the contract. This
// script computes it and checks a consumer's package.json against it.
//
//   node scripts/check-shared-frontend-deps.mjs                     # check this repo
//   node scripts/check-shared-frontend-deps.mjs ../deepwork-pro     # check a consumer
//
// Exit codes are graded, because the two halves differ in certainty and a consumer's build script
// should be able to act on that difference rather than treating every finding as fatal:
//   0  clean
//   1  ADVISORY — a declared-dependency gap. Vite bundles only what is reached, so a consumer that
//      never imports the subsystem needing that package builds fine without it.
//   2  FATAL — an @terminal/* import that no longer resolves here. This CANNOT build; failing now,
//      with the module name and the importing file, beats failing later inside another repo.
// Wire it into every consumer's frontend build (pro: scripts/build-frontend.sh).
import { readdir, readFile } from 'node:fs/promises'
import { join, dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const HERE = dirname(fileURLToPath(import.meta.url))
const TERMINAL_ROOT = resolve(HERE, '..')
const SRC = join(TERMINAL_ROOT, 'frontend', 'src')

// Import specifiers that are NOT npm packages: build-time aliases (resolved by each consumer's
// own vite config) and runtime-provided namespaces.
const NON_PACKAGE_PREFIXES = ['@terminal/', '@ce/', 'node:', 'bun:', 'virtual:']
const NON_PACKAGE_EXACT = new Set(['deepwork-terminal'])

async function* walk(dir) {
  for (const entry of await readdir(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name)
    if (entry.isDirectory()) yield* walk(path)
    else if (/\.(ts|tsx|vue|js|mjs)$/.test(entry.name)) yield path
  }
}

// Bare specifier → package name: `marked` → marked, `@xterm/addon-fit/lib/x` → @xterm/addon-fit,
// `highlight.js/styles/a.css` → highlight.js.
function packageOf(spec) {
  if (spec.startsWith('.') || spec.startsWith('/')) return null
  if (NON_PACKAGE_EXACT.has(spec)) return null
  if (NON_PACKAGE_PREFIXES.some((p) => spec.startsWith(p))) return null
  const parts = spec.split('/')
  return spec.startsWith('@') ? parts.slice(0, 2).join('/') : parts[0]
}

// Comments must go first: this codebase's comments are long, English, and full of the words
// "import" and "from" — a loose regex reads `"eventually, within maxWait of burst START"` as a
// package name, which makes the whole report untrustworthy.
function stripComments(text) {
  return text.replace(/\/\*[\s\S]*?\*\//g, '').replace(/(^|[^:'"`\\])\/\/[^\n]*/g, '$1')
}

// Only real module syntax, anchored on the statement keyword so prose can never match:
//   import x from 'p' / import 'p' / import('p') / export … from 'p' / require('p')
const IMPORT_RES = [
  /^[ \t]*import[\s\S]*?from\s*['"]([^'"]+)['"]/gm,
  /^[ \t]*import\s*['"]([^'"]+)['"]/gm,
  /^[ \t]*export[\s\S]*?from\s*['"]([^'"]+)['"]/gm,
  /\bimport\s*\(\s*['"]([^'"]+)['"]\s*\)/g,
  /\brequire\s*\(\s*['"]([^'"]+)['"]\s*\)/g,
]

const required = new Map() // package → one example file that needs it
for await (const file of walk(SRC)) {
  const text = stripComments(await readFile(file, 'utf8'))
  for (const re of IMPORT_RES) {
    for (const m of text.matchAll(re)) {
      const pkg = packageOf(m[1])
      if (pkg && !required.has(pkg)) required.set(pkg, file.slice(TERMINAL_ROOT.length + 1))
    }
  }
}

// ── half two of the contract: the module paths ───────────────────────────────────────────────
// A consumer reaches into this repo by path (`@terminal/composables/cli/useX`), so DELETING or
// renaming a file here breaks it just as surely as adding a dependency does — and more quietly,
// because the error names a file in a repo the developer isn't looking at. That is exactly how
// pro ended up unbuildable: 69b7658 removed usePushNotifications with Web Push, pro's main.ts kept
// importing it, and the embedded SPA silently stayed on its last successful build for ten days.
const RESOLVE_SUFFIXES = ['', '.ts', '.tsx', '.vue', '.js', '.mjs', '/index.ts', '/index.vue', '/index.js']

async function resolvesUnderTerminal(spec) {
  const rel = spec.slice('@terminal/'.length)
  for (const suffix of RESOLVE_SUFFIXES) {
    try {
      await readFile(join(SRC, rel + suffix))
      return true
    } catch { /* try the next extension */ }
  }
  return false
}

async function checkTerminalPaths(consumerSrc) {
  const broken = []
  let seen = 0
  const specs = new Map() // spec → first consumer file importing it
  for await (const file of walk(consumerSrc)) {
    const text = stripComments(await readFile(file, 'utf8'))
    for (const re of IMPORT_RES) {
      for (const m of text.matchAll(re)) {
        if (m[1].startsWith('@terminal/') && !specs.has(m[1])) specs.set(m[1], file)
      }
    }
  }
  for (const [spec, file] of specs) {
    seen++
    if (!(await resolvesUnderTerminal(spec))) broken.push({ spec, file })
  }
  return { seen, broken }
}

const consumerRoot = resolve(process.argv[2] ?? TERMINAL_ROOT)
const consumerPkgPath = join(consumerRoot, 'frontend', 'package.json')
let consumerPkg
try {
  consumerPkg = JSON.parse(await readFile(consumerPkgPath, 'utf8'))
} catch (e) {
  console.error(`cannot read ${consumerPkgPath}: ${e.message}`)
  process.exit(2)
}
const declared = { ...(consumerPkg.dependencies ?? {}), ...(consumerPkg.devDependencies ?? {}) }

// The version to ask for is the one THIS repo builds against — a consumer on a different major
// would be running the shared components against an API they were never compiled for.
const ownPkg = JSON.parse(await readFile(join(TERMINAL_ROOT, 'frontend', 'package.json'), 'utf8'))
const ownDeps = { ...(ownPkg.dependencies ?? {}), ...(ownPkg.devDependencies ?? {}) }

const missing = []
const mismatched = []
for (const [pkg, example] of [...required].sort()) {
  const want = ownDeps[pkg]
  if (!declared[pkg]) missing.push({ pkg, want, example })
  else if (want && declared[pkg] !== want) mismatched.push({ pkg, want, has: declared[pkg] })
}

const label = consumerRoot === TERMINAL_ROOT ? 'this repo' : consumerRoot
console.log(`shared frontend contract — consumer: ${label}`)
console.log(`  packages imported by frontend/src: ${required.size}`)

let worst = 0 // 0 clean · 1 advisory · 2 fatal

for (const { pkg, want, has } of mismatched) {
  console.log(`  ~ ${pkg}: consumer has ${has}, terminal builds against ${want}`)
}
if (missing.length) {
  worst = Math.max(worst, 1)
  console.error(`\n✗ missing ${missing.length} package(s) that frontend/src imports:`)
  for (const { pkg, want, example } of missing) {
    console.error(`  - ${pkg}${want ? `@${want}` : ''}   (imported by ${example})`)
  }
  console.error(`  fix: cd ${join(consumerRoot, 'frontend')} && npm install ${missing.map(({ pkg, want }) => (want ? `${pkg}@${want}` : pkg)).join(' ')} --save`)
  console.error(`  (a package the consumer never reaches is harmless to skip — vite only bundles what is imported)`)
} else {
  console.log(`  ✓ every imported package is declared`)
}

if (consumerRoot !== TERMINAL_ROOT) {
  const { seen, broken } = await checkTerminalPaths(join(consumerRoot, 'frontend', 'src'))
  console.log(`  @terminal/* modules referenced by the consumer: ${seen}`)
  if (broken.length) {
    worst = 2
    console.error(`\n✗ ${broken.length} @terminal/* import(s) no longer resolve in this repo:`)
    for (const { spec, file } of broken) {
      console.error(`  - ${spec}`)
      console.error(`      from ${file.slice(consumerRoot.length + 1)}`)
    }
    console.error(`  fix: the module was moved or deleted here — update the consumer, or restore it.`)
  } else {
    console.log(`  ✓ every @terminal/* import resolves`)
  }
}

process.exit(worst)
