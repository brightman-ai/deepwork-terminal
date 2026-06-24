<template>
  <div class="cli-tab-bar dw-titlebar-blend" data-testid="cli-portal-tab-bar">
    <!-- Groups + tabs -->
    <template v-for="group in groups" :key="group.id">
      <!-- Group header (only when multiple groups) -->
      <div
        v-if="showGroupHeaders"
        class="cli-tab-bar__group-header"
        :style="group.color ? { '--group-color': group.color } : {}"
        :data-testid="`cli-portal-group-header-${group.id}`"
        @click="emit('toggle-group', group.id)"
      >
        <span class="group-name">{{ group.name }}</span>
        <span class="group-chevron">{{ group.collapsed ? '▸' : '▾' }}</span>
      </div>

      <template v-if="!group.collapsed">
        <button
          v-for="tab in group.tabs"
          :key="tab.id"
          class="cli-tab-bar__tab"
          :class="{
            'is-active': tab.id === activeTabId,
            'needs-input': tabNeedsInput(tab.id),
          }"
          :data-testid="`cli-portal-tab-${tab.id}`"
          @click="emit('switch', tab.id)"
          @dblclick.stop="emit('rename-start', tab.id)"
        >
          <!-- Agent status dot -->
          <span class="tab-agent-dot" :class="tabDotClass(tab.id)" />

          <!-- Rename input -->
          <input
            v-if="renamingTabId === tab.id"
            :value="renameValue"
            class="tab-rename-input"
            :data-testid="`cli-portal-tab-rename-${tab.id}`"
            @input="emit('rename-input', ($event.target as HTMLInputElement).value)"
            @blur="emit('rename-commit')"
            @keyup.enter="emit('rename-commit')"
            @keyup.escape="emit('rename-cancel')"
            @keydown.stop
            @keypress.stop
            @click.stop
            @dblclick.stop
            @mousedown.stop
          />
          <span v-else class="tab-name">{{ tab.name }}</span>

          <!-- Close -->
          <span
            class="tab-close"
            :data-testid="`cli-portal-tab-close-${tab.id}`"
            @click.stop="emit('close', tab.id)"
          >&times;</span>
        </button>
      </template>
    </template>

    <!-- Add tab — branches into 本机 / 远程 via a small dropdown (Teleported so the tab bar's
         overflow:hidden can't clip it). -->
    <button
      ref="addBtnRef"
      class="cli-tab-bar__tab cli-tab-bar__tab--add"
      data-testid="cli-portal-add-tab"
      @click="toggleAddMenu"
    >+</button>
    <Teleport to="body">
      <div
        v-if="addMenuOpen"
        class="cli-add-menu-scrim"
        data-testid="cli-portal-add-menu"
        @click.self="addMenuOpen = false"
      >
        <div class="cli-add-menu" :style="addMenuStyle">
          <button class="cli-add-menu__item" data-testid="cli-portal-add-local" @click="pickAdd('local')">
            <Monitor :size="15" /><span>本机终端</span>
          </button>
          <button class="cli-add-menu__item" data-testid="cli-portal-add-remote" @click="pickAdd('remote')">
            <Server :size="15" /><span>远程终端…</span>
          </button>
        </div>
      </div>
    </Teleport>

    <!-- Spacer + right-side status (settings icon) -->
    <div class="cli-tab-bar__spacer" />
    <!-- Build version — unobtrusive, right-aligned (pinned right by the spacer, doesn't scroll
         with the tabs). Lets a user tell which release they're on without opening a terminal. -->
    <span
      v-if="versionLabel"
      class="cli-tab-bar__version"
      data-testid="cli-portal-version"
      :title="'deepwork-terminal ' + fullVersionLabel"
    >{{ versionLabel }}</span>
    <!-- PWA-only refresh: a standalone PWA has no address bar / F5, so a wedged state can't be
         reloaded. Force-fresh via /fresh (bypasses any stale cached index.html). -->
    <button
      v-if="isPWA"
      class="cli-tab-bar__refresh"
      type="button"
      data-testid="cli-portal-refresh"
      title="刷新（PWA 无 F5）"
      @click="onRefresh"
    ><RefreshCw :size="14" /></button>
    <slot name="status" />
  </div>
</template>

<script setup lang="ts">
import { computed, ref, onMounted } from 'vue'
import { RefreshCw, Monitor, Server } from 'lucide-vue-next'
import type { WorkbenchGroup } from '@terminal/types/workbench'
import { useCliAuth } from '@terminal/composables/cli/useCliAuth'
import { cliApi } from '@terminal/composables/cli/useCliApiPrefix'

interface TabRuntime {
  agentState: { status?: string } | null
  wsStatus: string
}

const props = defineProps<{
  groups: WorkbenchGroup[]
  activeTabId: string | undefined
  showGroupHeaders: boolean
  renamingTabId: string | null
  renameValue: string
  tabRuntimes: Record<string, TabRuntime>
}>()

const emit = defineEmits<{
  (e: 'switch', tabId: string): void
  (e: 'close', tabId: string): void
  (e: 'add'): void
  (e: 'add-remote'): void
  (e: 'rename-start', tabId: string): void
  (e: 'rename-input', value: string): void
  (e: 'rename-commit'): void
  (e: 'rename-cancel'): void
  (e: 'toggle-group', groupId: string): void
}>()

// Build version, fetched once from GET /version. Keep the badge short: release builds and
// dirty source builds both display as vX.Y.Z; the full string remains in the title.
const { cliFetch } = useCliAuth()
const version = ref('')
const fullVersionLabel = computed(() => formatFullVersion(version.value))
const versionLabel = computed(() => formatShortVersion(version.value))

function formatFullVersion(raw: string): string {
  const v = raw.trim()
  if (!v) return ''
  return /^\d/.test(v) ? 'v' + v : v
}

function formatShortVersion(raw: string): string {
  const full = formatFullVersion(raw)
  const match = full.match(/^v?(\d+\.\d+\.\d+)/)
  return match ? `v${match[1]}` : full
}

// Empty until fetched (and stays empty on failure → the badge hides).
onMounted(async () => {
  try {
    const r = await cliFetch(cliApi('/version'))
    if (r.ok) version.value = ((await r.json()) as { version?: string }).version ?? ''
  } catch { /* badge just stays hidden */ }
})

// ── Add-tab dropdown (本机 / 远程) ──
const addBtnRef = ref<HTMLButtonElement | null>(null)
const addMenuOpen = ref(false)
const addMenuStyle = ref<Record<string, string>>({})
function toggleAddMenu() {
  if (addMenuOpen.value) { addMenuOpen.value = false; return }
  const r = addBtnRef.value?.getBoundingClientRect()
  if (r) addMenuStyle.value = { top: `${r.bottom + 4}px`, left: `${r.left}px` }
  addMenuOpen.value = true
}
function pickAdd(which: 'local' | 'remote') {
  addMenuOpen.value = false
  if (which === 'local') emit('add')
  else emit('add-remote')
}

// A standalone PWA has no browser chrome (no address bar, no F5), so show an in-app refresh.
const isPWA = computed(
  () =>
    typeof window !== 'undefined' &&
    (window.matchMedia?.('(display-mode: standalone)').matches === true ||
      (window.navigator as { standalone?: boolean }).standalone === true),
)
async function onRefresh(): Promise<void> {
  // Belt-and-suspenders force-fresh for a wedged PWA:
  //   1. Drop any Cache Storage — a LEGACY caching service worker (an older build) may have
  //      precached the app shell and would otherwise keep shadowing the new build.
  //   2. Push every registered SW to update (update(), not unregister — keeps the push
  //      subscription alive).
  //   3. Navigate to /fresh, which the server 302-redirects to /?t=<unixnano> — a unique URL
  //      no cache can satisfy, so the no-cache index.html (+ its new hashed assets) is always
  //      fetched live. The current query (auth) is preserved.
  try {
    if (window.caches) {
      const keys = await caches.keys()
      await Promise.all(keys.map((k) => caches.delete(k)))
    }
    if (navigator.serviceWorker?.getRegistrations) {
      const regs = await navigator.serviceWorker.getRegistrations()
      for (const r of regs) { void r.update() }
    }
  } catch {
    /* best-effort — never block the reload on a cache-clear failure */
  }
  window.location.replace('/fresh' + window.location.search)
}

function tabDotClass(tabId: string): string {
  const rt = props.tabRuntimes[tabId]
  if (!rt) return 'dot-idle'
  const status = rt.agentState?.status
  if (status === 'running') return 'dot-running'
  if (status === 'waiting') return 'dot-waiting'
  if (rt.wsStatus === 'connected') return 'dot-connected'
  return 'dot-idle'
}

function tabNeedsInput(tabId: string): boolean {
  const rt = props.tabRuntimes[tabId]
  return rt?.agentState?.status === 'waiting'
}
</script>

<style scoped>
/* PWA-only refresh button — sits on the right, mirrors the add-tab affordance. */
.cli-tab-bar__refresh {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 34px;
  flex-shrink: 0;
  background: transparent;
  border: none;
  color: hsl(var(--muted-foreground));
  cursor: pointer;
}
.cli-tab-bar__refresh:hover { color: hsl(var(--foreground)); }
.cli-tab-bar__refresh:active { transform: scale(0.92); }

.cli-tab-bar {
  display: flex;
  align-items: stretch;
  height: 36px;
  background: hsl(var(--card));
  border-bottom: 1px solid hsl(var(--border));
  overflow-x: auto;
  overflow-y: hidden;
  flex-shrink: 0;
  scrollbar-width: none;
  -ms-overflow-style: none;
}
.cli-tab-bar::-webkit-scrollbar { display: none; }

/* Group header */
.cli-tab-bar__group-header {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 0 10px;
  font-size: 0.7rem;
  color: hsl(var(--muted-foreground));
  border-left: 3px solid var(--group-color, #4a9eff);
  cursor: pointer;
  white-space: nowrap;
  flex-shrink: 0;
  user-select: none;
  text-transform: uppercase;
  letter-spacing: 0.5px;
}
.cli-tab-bar__group-header:hover {
  background: hsl(var(--accent));
}
.group-chevron { font-size: 0.7rem; }

/* Tab */
.cli-tab-bar__tab {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 0 12px;
  min-width: 80px;
  max-width: 200px;
  height: 36px;
  background: transparent;
  border: none;
  border-bottom: 2px solid transparent;
  color: hsl(var(--muted-foreground));
  font-size: 0.8125rem;
  cursor: pointer;
  white-space: nowrap;
  overflow: hidden;
  flex-shrink: 0;
  transition: background 0.12s, color 0.12s;
  outline: none;
}
.cli-tab-bar__tab:hover {
  background: hsl(var(--accent));
  color: hsl(var(--foreground));
}
.cli-tab-bar__tab.is-active {
  border-bottom-color: #4a9eff;
  color: hsl(var(--foreground));
  background: hsl(var(--accent));
}
/* Pulse border on agent needs-input */
.cli-tab-bar__tab.needs-input {
  border-bottom-color: #ff9800;
  animation: tab-needs-input 1.5s infinite;
}
@keyframes tab-needs-input {
  0%, 100% { border-bottom-color: #ff9800; }
  50%       { border-bottom-color: rgba(255,152,0,0.3); }
}

.tab-name {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
}
.tab-rename-input {
  flex: 1;
  min-width: 40px;
  max-width: 120px;
  height: 22px;
  border: 1px solid hsl(var(--border));
  border-radius: 3px;
  background: hsl(var(--background));
  color: inherit;
  font: inherit;
  padding: 0 4px;
  outline: none;
}

/* Close (visible on hover/active) */
.tab-close {
  font-size: 1rem;
  line-height: 1;
  flex-shrink: 0;
  padding: 0 2px;
  border-radius: 3px;
  opacity: 0;
  margin-left: auto;
  color: hsl(var(--muted-foreground));
  transition: opacity 0.1s;
}
.cli-tab-bar__tab:hover .tab-close,
.cli-tab-bar__tab.is-active .tab-close { opacity: 0.6; }
.tab-close:hover { opacity: 1 !important; color: #ff6b6b; background: rgba(255,255,255,0.12); }

/* Agent dot */
.tab-agent-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  flex-shrink: 0;
}
.dot-idle      { background: #444; }
.dot-connected { background: #4caf50; }
.dot-running   { background: #4caf50; }
.dot-waiting   { background: #ff9800; }

/* Add button */
.cli-tab-bar__tab--add {
  min-width: 36px;
  max-width: 36px;
  font-size: 1.1rem;
  color: hsl(var(--muted-foreground));
  flex-shrink: 0;
  padding: 0;
  justify-content: center;
}
.cli-tab-bar__tab--add:hover { color: rgba(255,255,255,0.8); }

/* Spacer */
.cli-tab-bar__spacer { flex: 1; min-width: 8px; }

/* Build version — muted, unobtrusive, pinned right (after the spacer). */
.cli-tab-bar__version {
  display: inline-flex;
  align-items: center;
  flex-shrink: 0;
  padding: 0 10px;
  font-size: 0.62rem;
  letter-spacing: 0.3px;
  color: hsl(var(--muted-foreground));
  opacity: 0.55;
  white-space: nowrap;
  user-select: text;
}

/* Add-tab dropdown (Teleported to body; scoped styles still apply via the data-v attr). The
   full-screen scrim catches an outside-click to dismiss; the menu floats at the + button. */
.cli-add-menu-scrim {
  position: fixed;
  inset: 0;
  z-index: 900;
}
.cli-add-menu {
  position: fixed;
  min-width: 168px;
  display: flex;
  flex-direction: column;
  padding: 4px;
  background: hsl(var(--popover, 240 6% 12%));
  border: 1px solid hsl(var(--border, 240 4% 24%));
  border-radius: 10px;
  box-shadow: 0 12px 36px rgba(0, 0, 0, 0.5);
}
.cli-add-menu__item {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 9px 12px;
  background: transparent;
  border: none;
  border-radius: 7px;
  color: hsl(var(--foreground, 0 0% 92%));
  font-size: 0.85rem;
  text-align: left;
  cursor: pointer;
}
.cli-add-menu__item:hover { background: hsl(var(--accent, 240 4% 20%)); }
.cli-add-menu__item :deep(svg) { color: hsl(var(--muted-foreground)); flex-shrink: 0; }
</style>
