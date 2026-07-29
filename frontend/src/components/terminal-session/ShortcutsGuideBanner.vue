<script setup lang="ts">
// D6 — first-run shortcuts guide: a top banner shown once, collapsing into a small top-right
// icon after acknowledgement (same "loud once, then a quiet icon" pattern HelpCenter.vue already
// ships for its own first-run hint — SEEN_KEY + localStorage + dismiss-on-first-shortcut-use —
// copied here rather than reusing useAppUpdate's pill, which has no "acknowledged" state at all
// (see docs/topics/TH-20260728-cli-tabbar-convergence/session.md Round 2 for why).
import { ref, onMounted, onUnmounted } from 'vue'
import { Keyboard, X } from 'lucide-vue-next'

const props = defineProps<{
  /** Settings deep-link target for "查看/修改快捷键" — pushed by the host via router. */
  onOpenSettings: () => void
}>()

const SEEN_KEY = 'shortcuts_guide_seen'
const seen = ref(localStorage.getItem(SEEN_KEY) === '1')

// Teleport gate: a target absent on the first render frame silently no-ops and never retries,
// so only go inline AFTER mount (same reason HelpCenter does this). #dw-topbar-right exists in
// BOTH shells — standalone's CliTabBar declares it, pro's MainLayout mirrors it — which is why
// the collapsed icon can be shell-agnostic.
const ready = ref(false)
const hasOutlet = ref(false)

function dismiss(): void {
  seen.value = true
  localStorage.setItem(SEEN_KEY, '1')
}

// Any of our own tab shortcuts firing (Alt+digit/N/W/R/Up/Down) is itself proof the user found
// them — same "first use dismisses the nag" idea as HelpCenter's onFirstKey, but scoped to Alt
// so plain typing in the terminal doesn't spuriously dismiss it before the banner was ever read.
function onFirstAltKey(e: KeyboardEvent): void {
  if (!seen.value && e.altKey) dismiss()
}

onMounted(() => {
  if (!seen.value) window.addEventListener('keydown', onFirstAltKey, { capture: true })
  ready.value = true
  hasOutlet.value = !!document.getElementById('dw-topbar-right')
})
onUnmounted(() => window.removeEventListener('keydown', onFirstAltKey, { capture: true }))

function reopen(): void {
  seen.value = false
}
</script>

<template>
  <div>
    <Transition name="sgb-fade">
      <div v-if="!seen" class="sgb-banner" data-testid="shortcuts-guide-banner">
        <Keyboard :size="15" class="sgb-ico" />
        <span class="sgb-txt">试试 <kbd>Alt+1-9</kbd> 切换标签、<kbd>Alt+N</kbd> 新建、<kbd>Alt+W</kbd> 关闭——键位可在设置里改</span>
        <button class="sgb-btn" data-testid="shortcuts-guide-open-settings" @click="props.onOpenSettings(); dismiss()">去设置</button>
        <button class="sgb-btn primary" data-testid="shortcuts-guide-dismiss" @click="dismiss">知道了</button>
        <button class="sgb-x" title="不再提示" @click="dismiss"><X :size="13" /></button>
      </div>
    </Transition>
    <!-- Collapsed state: a small icon in the shared top-right chrome cluster. Falls back to a
         plain inline button when no outlet exists (a host that doesn't declare one). -->
    <Teleport v-if="seen && ready && hasOutlet" to="#dw-topbar-right">
      <button class="sgb-icon" title="快捷键指引" data-testid="shortcuts-guide-icon" @click="reopen">
        <Keyboard :size="14" />
      </button>
    </Teleport>
    <button
      v-else-if="seen && ready"
      class="sgb-icon"
      title="快捷键指引"
      data-testid="shortcuts-guide-icon"
      @click="reopen"
    ><Keyboard :size="14" /></button>
  </div>
</template>

<style scoped>
/* Fixed under the topbar rather than in normal flow: the two shells lay the CLI chrome out
   differently (standalone = a flex column owning its own tab row; pro = tabs teleported INTO the
   global topbar, with the portal shell at height:100%), so an in-flow banner rendered off-screen
   in pro. Viewport-anchored keeps one component correct in both. Top-center also avoids colliding
   with HelpCenter's first-run hint, which is pinned bottom-center. */
/* 靠右锚定，不居中：居中会正正压住终端最上面几行，而那里恰恰是最需要被读到的内容
   （自动重开留下的那行「上一个进程已随服务重启结束…」就是被它盖掉的，实测截图为证）。
   终端输出一律左对齐，首屏右半边几乎永远是空的，所以靠右既不遮字又照样醒目。
   底部居中已被 HelpCenter 的首次提示占用，不能往那挪。 */
.sgb-banner {
  position: fixed; top: calc(env(safe-area-inset-top, 0px) + 44px);
  right: 14px; z-index: 2400;
  display: flex; align-items: center; gap: 8px; max-width: min(94vw, 620px);
  padding: 7px 11px; font-size: 12.5px; border-radius: 10px;
  background: hsl(var(--card)); color: hsl(var(--foreground));
  border: 1px solid hsl(var(--border));
  box-shadow: 0 8px 30px rgba(0, 0, 0, 0.45);
}
.sgb-ico { color: #f59e0b; flex-shrink: 0; }
.sgb-txt { flex: 1; min-width: 0; }
.sgb-txt kbd {
  background: hsl(var(--background)); border: 1px solid hsl(var(--border));
  border-radius: 4px; padding: 1px 5px; font-size: 11.5px; font-family: monospace;
}
.sgb-btn {
  flex-shrink: 0; padding: 4px 10px; border-radius: 6px; font-size: 12px;
  border: 1px solid hsl(var(--border)); background: hsl(var(--background));
  color: hsl(var(--foreground)); cursor: pointer;
}
.sgb-btn.primary { background: #f59e0b; color: #16181d; border-color: #f59e0b; font-weight: 600; }
.sgb-x { flex-shrink: 0; background: transparent; border: none; color: hsl(var(--muted-foreground)); cursor: pointer; padding: 2px; }
.sgb-x:hover { color: hsl(var(--foreground)); }
.sgb-fade-enter-active, .sgb-fade-leave-active { transition: opacity 0.2s, max-height 0.2s; }
.sgb-fade-enter-from, .sgb-fade-leave-to { opacity: 0; }

.sgb-icon {
  display: inline-flex; align-items: center; justify-content: center;
  width: 26px; height: 26px; flex-shrink: 0; border-radius: 6px;
  color: hsl(var(--muted-foreground)); background: transparent; border: none; cursor: pointer;
}
.sgb-icon:hover { color: hsl(var(--foreground)); background: hsl(var(--accent)); }
</style>
