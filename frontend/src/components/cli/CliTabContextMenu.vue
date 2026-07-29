<script setup lang="ts">
/**
 * CliTabContextMenu — the floating menu itself. Pure presentation over useTabContextMenu's model.
 *
 * Shared by both shells rather than duplicated per tab bar: the two tab STRIPS differ structurally
 * (grouped vs flat) which is why they stayed separate components, but a floating menu anchored to a
 * pointer has no such difference — it is the same rectangle in both, so two copies would only be
 * two places for the wording and the edge-flipping to drift.
 *
 * Placement: `position: fixed` at the pointer, flipped when it would overflow the viewport. Fixed
 * (not absolute) because the tab strip is inside a horizontally scrollable container — an absolute
 * menu would scroll away from the pointer, and clipping is why it's teleported to <body>.
 */
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import type { TabMenuItem, TabMenuPosition } from '@terminal/composables/cli/useTabContextMenu'

const props = defineProps<{
  open: boolean
  items: TabMenuItem[]
  position: TabMenuPosition
  /** Shown as a dimmed header so you can tell WHICH tab you right-clicked — the pointer has
   *  already left the tab by the time the menu is up. */
  title?: string
}>()

const emit = defineEmits<{ (e: 'close'): void }>()

const menu = ref<HTMLElement | null>(null)
const measured = ref({ w: 0, h: 0 })

/** Keep the whole menu on screen: flip to the other side of the pointer near an edge, then clamp.
 *  Reads the real rendered size rather than assuming one, so a longer label can't push it off. */
const style = computed(() => {
  const pad = 8
  const vw = typeof window === 'undefined' ? 1024 : window.innerWidth
  const vh = typeof window === 'undefined' ? 768 : window.innerHeight
  const { w, h } = measured.value
  let left = props.position.x
  let top = props.position.y
  if (w && left + w + pad > vw) left = Math.max(pad, props.position.x - w)
  if (h && top + h + pad > vh) top = Math.max(pad, props.position.y - h)
  return { left: `${left}px`, top: `${top}px` }
})

watch(
  () => props.open,
  async (isOpen) => {
    if (!isOpen) return
    measured.value = { w: 0, h: 0 }
    await nextTick()
    const el = menu.value
    if (el) measured.value = { w: el.offsetWidth, h: el.offsetHeight }
    // Focus the menu so Escape and arrow-free keyboard dismissal work without a global handler.
    el?.focus()
  },
)

// Dismissal: anything that means "I'm doing something else now". pointerdown (not click) so the
// menu is gone before the underlying control reacts; capture so a stopPropagation elsewhere can't
// strand it open.
function onPointerDown(e: PointerEvent): void {
  if (!props.open) return
  if (menu.value?.contains(e.target as Node)) return
  emit('close')
}
function onKeyDown(e: KeyboardEvent): void {
  if (props.open && e.key === 'Escape') emit('close')
}
function onScrollOrResize(): void {
  if (props.open) emit('close')
}

watch(
  () => props.open,
  (isOpen) => {
    if (typeof document === 'undefined') return
    if (isOpen) {
      document.addEventListener('pointerdown', onPointerDown, true)
      document.addEventListener('keydown', onKeyDown, true)
      // capture:true — a scroll inside the tab strip doesn't bubble to window.
      window.addEventListener('scroll', onScrollOrResize, true)
      window.addEventListener('resize', onScrollOrResize)
      window.addEventListener('blur', onScrollOrResize)
    } else {
      teardown()
    }
  },
)

function teardown(): void {
  if (typeof document === 'undefined') return
  document.removeEventListener('pointerdown', onPointerDown, true)
  document.removeEventListener('keydown', onKeyDown, true)
  window.removeEventListener('scroll', onScrollOrResize, true)
  window.removeEventListener('resize', onScrollOrResize)
  window.removeEventListener('blur', onScrollOrResize)
}
onBeforeUnmount(teardown)

/** A separator goes ABOVE the first destructive entry, so "关闭" can't be hit by muscle memory
 *  aimed at the entry above it. */
function showsDivider(index: number): boolean {
  const item = props.items[index]
  return !!item?.danger && !props.items[index - 1]?.danger
}
</script>

<template>
  <Teleport to="body">
    <div
      v-if="open"
      ref="menu"
      class="tab-ctx"
      tabindex="-1"
      role="menu"
      data-testid="cli-tab-context-menu"
      :style="style"
      @contextmenu.prevent
    >
      <div v-if="title" class="tab-ctx__title">{{ title }}</div>
      <template v-for="(item, i) in items" :key="item.key">
        <div v-if="showsDivider(i)" class="tab-ctx__sep" />
        <button
          type="button"
          role="menuitem"
          class="tab-ctx__item"
          :class="{ 'is-danger': item.danger }"
          :disabled="item.disabled"
          :data-testid="`cli-tab-context-${item.key}`"
          @click="item.run()"
        >
          <span class="tab-ctx__label">{{ item.label }}</span>
          <span v-if="item.hint" class="tab-ctx__hint">{{ item.hint }}</span>
        </button>
      </template>
    </div>
  </Teleport>
</template>

<style scoped>
.tab-ctx {
  position: fixed;
  z-index: 1000;
  min-width: 168px;
  padding: 4px;
  border: 1px solid hsl(var(--border));
  border-radius: 8px;
  background: hsl(var(--card));
  box-shadow: 0 10px 28px rgba(0, 0, 0, 0.45);
  outline: none;
  animation: tab-ctx-in 0.09s ease-out;
}
@keyframes tab-ctx-in {
  from { opacity: 0; transform: scale(0.97); }
  to { opacity: 1; transform: none; }
}
@media (prefers-reduced-motion: reduce) {
  .tab-ctx { animation: none; }
}

.tab-ctx__title {
  padding: 5px 10px 6px;
  font-size: 0.68rem;
  color: hsl(var(--muted-foreground));
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  max-width: 240px;
}

.tab-ctx__sep {
  height: 1px;
  margin: 4px 6px;
  background: hsl(var(--border));
}

.tab-ctx__item {
  display: flex;
  align-items: center;
  gap: 16px;
  width: 100%;
  padding: 6px 10px;
  border: none;
  border-radius: 5px;
  background: transparent;
  color: hsl(var(--foreground));
  font-size: 0.82rem;
  text-align: left;
  cursor: pointer;
}
.tab-ctx__item:hover:not(:disabled) { background: hsl(var(--accent)); }
.tab-ctx__item:disabled { opacity: 0.4; cursor: default; }
.tab-ctx__item.is-danger:hover:not(:disabled) { background: rgba(255, 82, 82, 0.16); color: #ff6b6b; }

.tab-ctx__label { flex: 1; white-space: nowrap; }
.tab-ctx__hint {
  font-size: 0.68rem;
  color: hsl(var(--muted-foreground));
  white-space: nowrap;
}
</style>
