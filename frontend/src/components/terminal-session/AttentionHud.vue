<template>
  <!-- Teleported into the SSOT top-right outlet (HelpCenter's "?" already lands here) so this
       component works unchanged under any host shell that provides #dw-topbar-right (standalone
       CliTabBar today; pro's MainLayout is the same outlet id, ATT-11 — that shell's parity is a
       separate, Human-gated checkpoint, not this file's concern).
       The card itself is `position: fixed`, NOT relative to the outlet's own flex box: a topbar
       chrome cluster is the wrong shape to anchor a dropdown off (its box is whatever width its
       children happen to need), and floating a fixed-size popup off a near-zero-width anchor would
       either mis-align it or require guessing the row's height — the exact "magic number" mistake
       KC-5 undid for KeyCastr. `topOffset` is measured (never guessed) by the caller from real
       layout (see CliTerminalSurface's `headerBottom`), so the card floats clear of the pane bar's
       hit targets underneath it (ATT-10) on both mobile and PC without hand-typing a row height.

       `v-if="mounted"` is the teleport gate HelpCenter.vue:47-48 already documents the hard way: a
       target absent on the FIRST render frame makes Teleport silently no-op and never retry. Under
       standalone the surface renders after an async session fetch, so the outlet is always in the
       document by then and this looks unnecessary — but pro's shell mounts in a different order,
       and the failure mode there is a HUD that simply never appears with nothing logged (ATT-11).
       Cheap gate, un-diagnosable bug: gate it. -->
  <Teleport v-if="mounted" to="#dw-topbar-right">
    <Transition name="att-hud">
      <div
        v-if="card"
        class="att-hud-card"
        :class="statusClass"
        :style="cardStyle"
        role="button"
        tabindex="0"
        :aria-label="ariaLabel"
        data-testid="attention-hud-card"
        @click="onCardClick"
        @keyup.enter="onCardClick"
        @keyup.space.prevent="onCardClick"
        @touchstart="onTouchStart"
        @touchmove="onTouchMove"
        @touchend="onTouchEnd"
        @touchcancel="onTouchEnd"
      >
        <!-- ≥32px hit target (design requirement) even though the visible glyph is tiny. -->
        <button
          type="button"
          class="att-hud-close"
          title="关闭提醒"
          aria-label="关闭提醒"
          data-testid="attention-hud-dismiss"
          @click.stop="onDismissClick"
        >✕</button>

        <div class="att-hud-row1">
          <span class="att-hud-icon" aria-hidden="true">{{ icon }}</span>
          <template v-if="isMerged">
            <span class="att-hud-title">{{ title }}</span>
          </template>
          <template v-else>
            <span class="att-hud-idx">{{ card.primary.index }}</span>
            <span class="att-hud-name">{{ card.primary.name || '#' + card.primary.index }}</span>
            <span v-if="card.primary.tool" class="att-hud-tool">{{ card.primary.tool }}</span>
          </template>
        </div>
        <div class="att-hud-row2">
          <span class="att-hud-line">{{ line2 }}</span>
          <span class="att-hud-arrow" aria-hidden="true">→</span>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
/**
 * AttentionHud — the RENDER half of the attention HUD (ATT-2/3/4/7/9/10). All trigger / merge /
 * anti-spam logic lives in `useAttentionHud` (CP3); this file owns none of it — it just draws
 * whatever `card` the caller hands it and reports two intents back up: `activate` (tapped the
 * card) and `dismiss` (✕ or a right-swipe). The caller (CliTerminalSurface) holds the actual
 * `useAttentionHud()` instance, exactly the division AgentOverview.vue already uses for its own
 * cards (dumb renderer; the parent owns `useAgentOverview` + does selectWindow/dismiss).
 *
 * The auto-collapse timer (ATT-4, 8s) is NOT here — it lives entirely inside `useAttentionHud`,
 * which flips `card` back to null on its own; collapsing never emits `dismiss`, so it can never
 * write "seen" (that would silently break the ATT-4 contract from the render side). This
 * component only ever reacts to that prop changing. The `<Transition>` wrapping a `v-if` bound to
 * external state IS the animation skeleton `TmuxPaneBar.vue`'s `tpb-hint` already established
 * (one-shot hint, auto-dismissed by its owner, fades via opacity+transform, same duration/easing)
 * — reused here, not redesigned; only the slide axis is vertical (down, out from under the
 * topbar) instead of tpb-hint's horizontal one, because this HUD drops from a different edge.
 */
import { computed, onMounted, ref, watch } from 'vue'
import { mergedHeadline, type AttentionCard } from '@terminal/composables/cli/useAttentionHud'
import { STATUS_COLOR } from '@terminal/composables/cli/useAgentOverview'

// Teleport gate — see the template comment. Same `ready`-flag idiom HelpCenter.vue uses, rather
// than Vue 3.5's `<Teleport defer>`: package.json declares `vue: ^3.4.0`, and on a 3.4 resolution
// `defer` is an unknown attribute that is silently ignored — which reinstates the exact bug this
// fixes, invisibly. The flag works on every version in the declared range.
const mounted = ref(false)
onMounted(() => { mounted.value = true })

const props = defineProps<{
  card: AttentionCard | null
  /** Bottom edge (viewport px) of this surface's ENTIRE header stack — topbar + its own pane-bar/
   *  status row — as measured by the caller (never guessed; see CliTerminalSurface's
   *  `headerBottom`). Absent/0 only before the first real layout measurement lands. */
  topOffset?: number
}>()

const emit = defineEmits<{
  /** Tapped the card: caller must call its hud.activate() + selectWindow(target.index). */
  (e: 'activate'): void
  /** ✕ or a committed right-swipe: caller must call its hud.dismiss(). */
  (e: 'dismiss'): void
}>()

const cardTop = computed(() => Math.max(0, props.topOffset ?? 0) + 8)

const isMerged = computed(() => (props.card?.items.length ?? 0) > 1)

// Icon / left-stripe / wording ALL key off the PRIMARY item (the one a tap jumps to) — including
// merged cards, which used to be special-cased to a hardcoded ⏳ + "N 个窗口等你" no matter what
// they actually held. Removing that special case is what fixes the ✓ case for free: `items` is
// sorted by URGENCY_ORDER, so a card containing any waiting window has a waiting primary (⏳) and
// an all-done card has a done-unseen primary (✓). One rule, no mix-dependent branch to keep in
// sync. The headline's honesty about the mix lives in `mergedHeadline` — see its doc comment for
// why a merged card must not report one flat total.
const icon = computed(() => (props.card?.primary.status === 'waiting' ? '⏳' : '✓'))
const statusClass = computed(() => `s-${props.card?.primary.status ?? 'waiting'}`)

const title = computed(() => {
  const c = props.card
  if (!c) return ''
  return isMerged.value ? mergedHeadline(c.items) : `窗口 ${c.primary.index} ${c.primary.name}`
})

const line2 = computed(() => {
  const c = props.card
  if (!c) return ''
  if (c.items.length > 1) return c.items.map((it) => `窗口${it.index}`).join(' · ')
  return c.primary.status === 'waiting' ? '在等你输入' : '跑完了'
})

// Reads the SAME `title` the sighted card shows — it used to restate the merged headline inline,
// which is how a screen reader ends up describing a card that no longer exists.
const ariaLabel = computed(() => {
  if (!props.card) return ''
  return `${title.value}，${line2.value}。点击跳转，或使用右上角按钮关闭`
})

// ── mobile right-swipe-to-dismiss ──────────────────────────────────────────────────────────
// Deliberately minimal: only rightward motion counts, the card visually trails the finger
// (immediate feedback — no "did my swipe register?" doubt) and springs back on a short/aborted
// drag rather than risk dismissing a tap that was really meant to activate. A committed swipe
// (past SWIPE_THRESHOLD) fires the exact same `dismiss` the ✕ button does — one intent behind two
// gestures, not two different behaviors wearing one label. A swipe that DID cross the threshold
// suppresses the synthetic click browsers fire right after touchend, so a dismiss-swipe can never
// also fire `activate` a beat later.
const SWIPE_THRESHOLD = 64
const dragX = ref(0)
let touchStartX = 0
let dragging = false
let suppressNextClick = false

// The suppression flag CANNOT be left to the synthetic click to clear. Once a touch has moved past
// the engine's slop threshold (~8px in Chrome; our own commit threshold is 64px, well past it) most
// engines dispatch NO click at all after touchend — so the flag set by a committed swipe would stay
// true forever, and this component never unmounts (the `v-if` is on the inner div), which means the
// NEXT card's first tap gets eaten with zero feedback. A new card is by definition a new gesture,
// so reset there: the flag then only ever spans the touchend→click of one single card.
// Guarded on a NON-null card on purpose: a committed swipe nulls the card first, and clearing the
// flag on that transition could re-open the window for a late synthetic click to fire `activate`
// right after a `dismiss`. While `card` is null there is nothing to click, so leaving it set costs
// nothing; the reset that matters is the one on the next card's arrival.
watch(() => props.card, (c) => { if (c) suppressNextClick = false })

const cardStyle = computed(() => {
  const style: Record<string, string> = { top: cardTop.value + 'px' }
  if (dragging && dragX.value > 0) {
    style.transform = `translateX(${dragX.value}px)`
    style.opacity = String(Math.max(0.2, 1 - dragX.value / 160))
    style.transition = 'none'
  }
  return style
})

function onCardClick(): void {
  if (suppressNextClick) {
    suppressNextClick = false
    return
  }
  emit('activate')
}
function onDismissClick(): void {
  emit('dismiss')
}

function onTouchStart(e: TouchEvent): void {
  touchStartX = e.touches[0]?.clientX ?? 0
  dragging = true
  dragX.value = 0
}
function onTouchMove(e: TouchEvent): void {
  if (!dragging) return
  const dx = (e.touches[0]?.clientX ?? touchStartX) - touchStartX
  dragX.value = Math.max(0, dx) // rightward only — leftward drag does nothing (not a gesture we own)
}
function onTouchEnd(): void {
  if (!dragging) return
  dragging = false
  const committed = dragX.value >= SWIPE_THRESHOLD
  dragX.value = 0
  if (committed) {
    suppressNextClick = true
    emit('dismiss')
  }
}
</script>

<style scoped>
/* `position: fixed` — see the template comment for why this floats off the measured
   `topOffset` instead of the teleport target's own (near-zero) box. Right-anchored to sit under
   the topbar's own right-aligned chrome cluster; capped width keeps it a glanceable card, never a
   banner. z-index sits in the same "normal float" tier as KeyCastr's pills (200) and above the
   pane-bar's own tip (4000 is a hover tooltip, transient and never up at the same time in
   practice) — comfortably below any modal sheet (help-sheet scrim is 2600+). */
.att-hud-card {
  --status-waiting: v-bind('STATUS_COLOR.waiting');
  --status-done: v-bind("STATUS_COLOR['done-unseen']");
  position: fixed;
  right: 8px;
  z-index: 220;
  width: max-content;
  max-width: 280px;
  box-sizing: border-box;
  padding: 9px 34px 9px 11px;
  display: flex;
  flex-direction: column;
  gap: 5px;
  background: #221636;
  border: 1px solid #3a2860;
  border-left: 3px solid #3a2860;
  border-radius: 8px;
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.45);
  color: #d8c8ee;
  cursor: pointer;
  touch-action: pan-y; /* vertical page scroll passes through untouched; we only read horizontal drag */
  user-select: none;
  -webkit-user-select: none;
}
.att-hud-card:hover { background: #2a1c44; border-color: #4a3570; }
.att-hud-card:active { background: #301f4e; }
/* Left stripe = STATUS_COLOR SSOT, same 3px-stripe idiom AgentOverview's .ao-card already uses —
   not a new color language, the same one. */
.att-hud-card.s-waiting { border-left-color: var(--status-waiting); }
.att-hud-card.s-done-unseen { border-left-color: var(--status-done); }

@media (max-width: 480px) {
  .att-hud-card { max-width: calc(100vw - 24px); }
}

.att-hud-close {
  position: absolute;
  top: 0;
  right: 0;
  width: 32px;
  height: 32px;
  display: inline-grid;
  place-items: center;
  background: transparent;
  border: none;
  border-radius: 0 8px 0 8px;
  color: #8a7aa8;
  font-size: 13px;
  line-height: 1;
  cursor: pointer;
  touch-action: manipulation;
}
.att-hud-close:hover, .att-hud-close:focus-visible { background: rgba(255, 255, 255, 0.08); color: #f0e0ff; }
.att-hud-close:active { transform: scale(0.92); }

.att-hud-row1 { display: flex; align-items: center; gap: 6px; min-width: 0; }
.att-hud-icon { flex-shrink: 0; font-size: 13px; line-height: 1; }
/* Same badge look as `.tpb-idx` / `.ao-idx` — same window number, same source, same face. */
.att-hud-idx {
  flex-shrink: 0;
  min-width: 1.3em;
  padding: 0 4px;
  border-radius: 4px;
  background: #2a1f3a;
  border: 1px solid #3a2860;
  color: #b08fd0;
  font-family: var(--dw-mono, ui-monospace, monospace);
  font-size: 0.62rem;
  font-weight: 700;
  line-height: 1.55;
  text-align: center;
  font-variant-numeric: tabular-nums;
}
.att-hud-name, .att-hud-title {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 0.82rem;
  font-weight: 700;
  color: #f0e0ff;
}
.att-hud-tool {
  flex-shrink: 0;
  padding: 1px 6px;
  border-radius: 4px;
  background: #16121f;
  border: 1px solid #3a2860;
  color: #8a7aa8;
  font-family: var(--dw-mono, ui-monospace, monospace);
  font-size: 0.6rem;
  font-weight: 600;
}
.att-hud-row2 { display: flex; align-items: center; gap: 6px; font-size: 0.72rem; color: #b8a8d8; }
.att-hud-line { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.att-hud-arrow { flex-shrink: 0; color: #8a7aa8; }

.att-hud-enter-active, .att-hud-leave-active { transition: opacity 0.2s ease, transform 0.2s ease; }
.att-hud-enter-from, .att-hud-leave-to { opacity: 0; transform: translateY(-6px); }

@media (prefers-reduced-motion: reduce) {
  .att-hud-enter-active, .att-hud-leave-active { transition: opacity 0.2s ease; }
  .att-hud-enter-from, .att-hud-leave-to { transform: none; }
}
</style>
