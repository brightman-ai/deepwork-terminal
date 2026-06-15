// UX inspection harness — iPhone 14 (webkit) against a live dw-terminal.
// Usage: node ux-inspect.mjs <baseUrl> <outDir> <step>
//   step = nav | compose | all
import pw from '/home/ubuntu/code/deepwork/node_modules/playwright/index.js'
import { mkdirSync } from 'node:fs'
const { webkit, devices } = pw

const BASE = process.argv[2] || 'http://127.0.0.1:8087/?auth=accept123'
const OUT = process.argv[3] || '/home/ubuntu/code/stwork/deepwork-terminal/docs/ux-overhaul/accept'
const STEP = process.argv[4] || 'all'
mkdirSync(OUT, { recursive: true })

const iphone = devices['iPhone 14']

function log(...a) { console.log(...a) }

async function rect(loc) {
  try { return await loc.first().boundingBox() } catch { return null }
}

const browser = await webkit.launch()
const ctx = await browser.newContext({ ...iphone })
const pg = await ctx.newPage()
pg.on('console', m => { const t = m.text(); if (/error|warn/i.test(m.type())) log('  [console.' + m.type() + ']', t.slice(0, 200)) })
pg.on('pageerror', e => log('  [pageerror]', String(e).slice(0, 200)))

log('== goto', BASE)
await pg.goto(BASE, { waitUntil: 'networkidle', timeout: 30000 }).catch(e => log('goto err', e.message))
await pg.waitForTimeout(1500)

// Simulate iPhone 14 safe-area insets (webkit headless reports env() as 0px). This
// reproduces the real-device / PWA-standalone condition under which the bugs appear:
// notch top inset + home-indicator bottom inset.
if (process.env.SIM_INSETS === '1') {
  await pg.addStyleTag({ content: `
    [data-testid="main-layout"].dw-app-viewport-frame{ padding-top:47px !important; }
    .is-mobile .bottom-bar{ padding-bottom:34px !important; }
  ` })
  await pg.waitForTimeout(300)
  log('-- simulated iPhone 14 insets injected (top47 / bottom34)')
}

const vp = pg.viewportSize()
log('viewport', JSON.stringify(vp))

// ---------- BUG 1: nav / settings ----------
if (STEP === 'nav' || STEP === 'all') {
  log('\n=== BUG1: NAV / SETTINGS ===')
  // Is the mobile portal-nav trigger present?
  const trig = pg.locator('[data-testid="mobile-portal-nav-trigger"]')
  log('mobile-portal-nav-trigger count =', await trig.count(), 'rect=', JSON.stringify(await rect(trig)))
  // Is a settings item already in the DOM (rail may be in a closed Sheet)?
  const navSettings = pg.locator('[data-testid="nav-item-settings"]')
  log('nav-item-settings count(before open) =', await navSettings.count())
  // Open the rail sheet
  if (await trig.count()) {
    await trig.first().click({ force: true }).catch(e => log('trig click err', e.message))
    await pg.waitForTimeout(800)
  }
  const railEl = pg.locator('[data-testid="navigation-sidebar"]')
  log('navigation-sidebar count(after open) =', await railEl.count(), 'visible=', await railEl.first().isVisible().catch(() => false))
  log('nav-item-settings count(after open) =', await navSettings.count())
  const setVisible = await navSettings.first().isVisible().catch(() => false)
  log('nav-item-settings visible =', setVisible, 'rect=', JSON.stringify(await rect(navSettings)))
  // list all nav items in the rail
  const items = pg.locator('[data-testid^="nav-item-"]')
  const n = await items.count()
  const names = []
  for (let i = 0; i < n; i++) names.push(await items.nth(i).getAttribute('data-testid'))
  log('all nav items =', JSON.stringify(names))
  await pg.screenshot({ path: OUT + '/nav-compose-1-rail-open.png' })
  log('shot -> nav-compose-1-rail-open.png')
  // try tapping settings
  if (setVisible) {
    await navSettings.first().click({ force: true }).catch(e => log('settings click err', e.message))
    await pg.waitForTimeout(1200)
    log('after tap settings, url =', pg.url())
    await pg.screenshot({ path: OUT + '/nav-compose-2-settings-page.png' })
    log('shot -> nav-compose-2-settings-page.png')
  } else {
    log('!! settings NOT visible/tappable in rail')
  }
  // navigate back to cli for compose test
  await pg.goto(BASE.replace(/\/\?/, '/portal/cli?'), { waitUntil: 'networkidle' }).catch(() => {})
  await pg.waitForTimeout(1000)
}

// ---------- BUG 2: compose toolbar occlusion ----------
if (STEP === 'compose' || STEP === 'all') {
  log('\n=== BUG2: COMPOSE TOOLBAR ===')
  // Reproduce a real iPhone home-indicator inset (webkit headless reports 0px for
  // env(safe-area-inset-bottom)). Force a 34px bottom inset like an iPhone 14.
  await pg.addStyleTag({ content: ':root{ --force-safe-bottom: 34px; }' })
  await pg.evaluate(() => {
    // Override env() consumers by adding an explicit fallback test: paint a marker
    // and also set a CSS var many components read; primarily we rely on the real
    // env value, but log what the browser resolves.
    const probe = document.createElement('div')
    probe.style.cssText = 'position:fixed;bottom:0;height:env(safe-area-inset-bottom,0px);width:1px'
    document.body.appendChild(probe)
    ;(window).__safeBottom = getComputedStyle(probe).height
  })
  log('resolved env(safe-area-inset-bottom) =', await pg.evaluate(() => (window).__safeBottom))
  // ensure we are on cli portal
  if (!/portal\/cli/.test(pg.url())) {
    await pg.goto(BASE.replace(/\/?(\?|$)/, '/portal/cli$1'), { waitUntil: 'networkidle' }).catch(() => {})
    await pg.waitForTimeout(1200)
  }
  // Dump candidate bottom-chrome testids
  const candidateSels = [
    '[data-testid*="compose"]', '[data-testid*="Compose"]',
    '[data-testid*="toolbar"]', '[data-testid*="quick"]',
    '[data-testid*="tmux"]', '[data-testid*="key"]',
  ]
  for (const s of candidateSels) {
    const c = await pg.locator(s).count()
    if (c) {
      for (let i = 0; i < c; i++) {
        const el = pg.locator(s).nth(i)
        const tid = await el.getAttribute('data-testid')
        log('  found', s, '->', tid, 'rect=', JSON.stringify(await rect(el)))
      }
    }
  }
  // Find the "Compose" toggle button in the main Toolbar (title="Compose")
  const composeTrigger = pg.locator('button[title="Compose"]')
  log('compose trigger (title=Compose) count =', await composeTrigger.count())
  if (await composeTrigger.count()) {
    await composeTrigger.first().click({ force: true }).catch(e => log('compose trigger err', e.message))
    await pg.waitForTimeout(900)
  }
  await pg.screenshot({ path: OUT + '/nav-compose-3-compose-open.png', fullPage: false })
  log('shot -> nav-compose-3-compose-open.png')
  // measure compose bar vs bottom chrome
  const compose = pg.locator('.compose-bar')
  log('compose-bar count =', await compose.count(), 'rect=', JSON.stringify(await rect(compose)))
  const sendBtn = pg.locator('.btn-send')
  log('btn-send rect=', JSON.stringify(await rect(sendBtn)))
  const inputRow = pg.locator('.compose-input-row')
  log('compose-input-row rect=', JSON.stringify(await rect(inputRow)))
  // bottom-bar geometry + how much of compose is below the visual viewport
  const bbGeo = await pg.evaluate(() => {
    const vh = window.visualViewport?.height ?? window.innerHeight
    const bb = document.querySelector('.bottom-bar')
    const cb = document.querySelector('.compose-bar')
    const tb = document.querySelector('.bottom-bar .tb-row, .bottom-bar [class*="toolbar"]')
    const send = document.querySelector('.btn-send')
    const r = el => { if (!el) return null; const x = el.getBoundingClientRect(); return { top: Math.round(x.top), bottom: Math.round(x.bottom), h: Math.round(x.height) } }
    const cs = bb ? getComputedStyle(bb) : null
    return { vh: Math.round(vh), bottomBar: r(bb), bottomBarPB: cs?.paddingBottom, composeBar: r(cb), sendBtn: r(send),
      sendClippedByVH: send ? Math.round(send.getBoundingClientRect().bottom - vh) : null }
  })
  log('bottom-bar visual analysis:', JSON.stringify(bbGeo, null, 0))
  // Report geometry of all fixed/sticky bottom elements
  const geo = await pg.evaluate(() => {
    const out = []
    const all = document.querySelectorAll('[data-testid],[class*="compose"],[class*="toolbar"],[class*="quick"],[class*="tmux"]')
    const vh = window.visualViewport?.height ?? window.innerHeight
    for (const el of all) {
      const cs = getComputedStyle(el)
      if (cs.position !== 'fixed' && cs.position !== 'sticky' && cs.position !== 'absolute') continue
      const r = el.getBoundingClientRect()
      if (r.height === 0 || r.width === 0) continue
      if (r.bottom < vh * 0.4) continue // only bottom-ish chrome
      out.push({
        tid: el.getAttribute('data-testid') || '',
        cls: (el.className || '').toString().slice(0, 60),
        pos: cs.position, z: cs.zIndex,
        top: Math.round(r.top), bottom: Math.round(r.bottom), h: Math.round(r.height),
        pb: cs.paddingBottom,
      })
    }
    return { vh: Math.round(vh), innerH: window.innerHeight, items: out.sort((a, b) => a.top - b.top) }
  })
  log('viewport visual height =', geo.vh, 'innerHeight =', geo.innerH)
  log('bottom-chrome stacking (top->bottom):')
  for (const it of geo.items) log('   ', JSON.stringify(it))
}

await browser.close()
log('\n== done')
