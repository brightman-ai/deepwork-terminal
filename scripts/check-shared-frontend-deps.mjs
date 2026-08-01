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
// Exit codes are graded, because the halves differ in certainty and a consumer's build script
// should be able to act on that difference rather than treating every finding as fatal:
//   0  clean
//   1  ADVISORY — a declared-dependency gap. Vite bundles only what is reached, so a consumer that
//      never imports the subsystem needing that package builds fine without it.
//   2  FATAL — an @terminal/* import that no longer resolves here, or an API endpoint the shared
//      frontend calls that THIS server does not route. Neither can work; failing now, with the
//      module or URL and the calling file, beats failing later inside another repo or at runtime.
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

// Directories with nothing authored in them. Skipping node_modules is not just speed: a
// dependency's own source would otherwise be read as if this repo had written it.
const SKIP_DIRS = new Set(['node_modules', '.git', 'dist', 'refs', 'testdata'])

async function* walk(dir, pattern = /\.(ts|tsx|vue|js|mjs)$/) {
  for (const entry of await readdir(dir, { withFileTypes: true })) {
    if (SKIP_DIRS.has(entry.name)) continue
    const path = join(dir, entry.name)
    if (entry.isDirectory()) yield* walk(path, pattern)
    else if (pattern.test(entry.name)) yield path
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

// ── half three of the contract: the API endpoints ────────────────────────────────────────────
// The first two halves check what the frontend needs at BUILD time. This one checks what it
// needs at RUN time, and it is the half whose absence has hurt twice in one week — both times
// the same way, both times silently:
//
//   /telemetry/log            → 404, so every structured frontend log line was dropped on the floor
//   /browser/clipboard/files  → 404, read by the paste resolver as "no files on the clipboard",
//                               so a local paste fell back to uploading and hit the size cap
//
// Neither failed loudly, because a 404 is a perfectly good answer to a probe. Both existed in
// deepwork-pro and were missing HERE — which is the shape of the whole problem: the frontend is
// written against the union of what its two hosts serve, and nothing checked either host against
// it. So: whatever `frontend/src` fetches IS the contract, the same way its imports are, and
// this derives it from the source rather than from a list someone has to remember to update.
//
// Scope note: paths only, not methods. A call site states its path in one string and its method
// in an options object several lines away, so pairing them statically would mean guessing. A
// wrong-method call gets a 405 — visible, debuggable — while a wrong path gets a silent 404,
// and it is the silent one this is for.
const API_CALL_RES = [
  // cliApi('/x') — the normal call; carries the host's mount prefix (pro embeds at /cli).
  /\bcliApi\(\s*[`'"]([^`'"]+)[`'"]/g,
  // peerApi('/x') — another terminal instance's API, always mounted at /api.
  /\bpeerApi\(\s*[`'"]([^`'"]+)[`'"]/g,
  // cliFetch('/api/x') — the prefix written out by hand. This is the form that hid
  // /browser/clipboard/files: it does not go through cliApi, so it reads as a URL, not a route.
  /\bcliFetch\(\s*[`'"](\/api\/[^`'"]+)[`'"]/g,
]

// Go's ServeMux patterns: `mux.HandleFunc("GET /sessions/{id}/ws", …)`.
const GO_ROUTE_RE = /mux\.HandleFunc\(\s*"(?:([A-Z]+)\s+)?(\/[^"]*)"/g

// A path reduced to what matters for matching: query gone, template holes and route wildcards
// both collapsed to a single token, so `/sessions/${id}/input` and `/sessions/{id}/input` are
// the same shape. Trailing slashes go too — Go's mux treats `/x` and `/x/` as one route here.
function normalizePath(path) {
  return path
    .split('?')[0]
    .replace(/^\/api/, '')
    .replace(/\$\{[^}]*\}/g, '{}')
    .replace(/\{[^}]*\}/g, '{}')
    .replace(/\/+$/, '') || '/'
}

// A hole matches anything on EITHER side. On the route side that is Go's `{id}` wildcard;
// on the call side it is a runtime value the source does not spell out — `cliApi(`/files/
// ${op}`)` where op is one of mkdir/create/rename/delete, all of them routed. Treating the
// call's hole as a literal would report that as unrouted, which is not just noise: a check
// that cries wolf gets an allowlist bolted onto it, and then it is a hand-maintained list
// again. The real defects all have a LITERAL tail (/telemetry/log, /browser/clipboard/files,
// /sessions/{}/agent-state), so nothing this exists to catch is lost by the relaxation.
function routeServes(routeSegments, callSegments) {
  if (routeSegments.length !== callSegments.length) return false
  return routeSegments.every((seg, i) => seg === '{}' || callSegments[i] === '{}' || seg === callSegments[i])
}

async function collectGoRoutes(root) {
  const routes = []
  for await (const file of walk(root, /\.go$/)) {
    if (file.endsWith('_test.go')) continue
    const text = await readFile(file, 'utf8')
    for (const m of text.matchAll(GO_ROUTE_RE)) {
      routes.push(normalizePath(m[2]).split('/'))
    }
  }
  return routes
}

async function checkApiEndpoints(root) {
  const routes = await collectGoRoutes(root)
  const calls = new Map() // normalized path → first file calling it
  for await (const file of walk(join(root, 'frontend', 'src'))) {
    if (file.includes('__tests__')) continue // a mock URL is not a claim about the server
    const text = stripComments(await readFile(file, 'utf8'))
    for (const re of API_CALL_RES) {
      for (const m of text.matchAll(re)) {
        const path = normalizePath(m[1])
        if (!calls.has(path)) calls.set(path, file.slice(root.length + 1))
      }
    }
  }
  const unrouted = []
  for (const [path, file] of calls) {
    if (!routes.some((route) => routeServes(route, path.split('/')))) unrouted.push({ path, file })
  }
  return { routes: routes.length, calls: calls.size, unrouted }
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

// The endpoint half is always checked against THIS repo, whoever the consumer is: it asks
// "does the standalone terminal serve what its own frontend calls", which is a property of this
// repo alone. A consumer that mounts the same frontend must answer the same question about its
// own routes — pro serves them from a gin router, so the check would have to learn that shape
// before it can be pointed there.
{
  const { routes, calls, unrouted } = await checkApiEndpoints(TERMINAL_ROOT)
  console.log(`  API endpoints called by frontend/src: ${calls} (against ${routes} routes)`)
  if (unrouted.length) {
    // FATAL for this repo's own check, ADVISORY when a consumer runs it. The gap is always
    // real, but only this repo can close it — blocking pro's frontend build on a route
    // missing in ANOTHER repo would stop the wrong person, mid-task, over something they
    // cannot fix from where they are standing.
    worst = Math.max(worst, consumerRoot === TERMINAL_ROOT ? 2 : 1)
    console.error(`\n✗ ${unrouted.length} endpoint(s) the frontend calls that this server does not route:`)
    for (const { path, file } of unrouted) {
      console.error(`  - ${path}`)
      console.error(`      from ${file}`)
    }
    console.error(`  fix: register the route (server.go), or stop calling it. A missing route answers 404,`)
    console.error(`       which most callers here read as an empty result — the failure is silent, not loud.`)
  } else {
    console.log(`  ✓ every endpoint the frontend calls is routed`)
  }
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
