<script setup lang="ts">
import { computed } from 'vue'
import type { QuickSwitchItem } from '../../composables/cli/useTabQuickSwitch'
import type { OverviewUnit } from '../../composables/cli/useAgentOverview'
import PaneStatusIndicator from './PaneStatusIndicator.vue'

const props = defineProps<{ items: QuickSwitchItem[]; units: OverviewUnit[]; selectedId: string; activeId?: string }>()
const emit = defineEmits<{ (e: 'select', id: string): void; (e: 'pane', id: string, sessionId: string): void }>()
const bySession = computed(() => new Map(props.units.map(unit => [unit.key, unit])))
function unit(sessionId?: string): OverviewUnit | undefined { return sessionId ? bySession.value.get(sessionId) : undefined }
function tail(sessionId?: string): string[] {
  const lines = unit(sessionId)?.tail.slice() ?? []
  while (lines.length && !lines[lines.length - 1]?.trim()) lines.pop()
  return lines.slice(-5)
}
</script>

<template>
  <div class="observation-cards" data-testid="tab-observation-cards">
    <article v-for="item in items" :key="item.id" class="observation-card" :class="{ selected: item.id === selectedId }" :data-tab-id="item.id">
      <button type="button" class="observation-card__heading" :data-switcher-tab="item.id" :data-testid="`observation-tab-${item.id}`" @mousedown.prevent @click="emit('select', item.id)">
        <span class="observation-card__number">{{ item.position ?? '·' }}</span><strong :title="item.name">{{ item.name }}</strong><small v-if="item.id === activeId">当前</small><span class="observation-card__engine">{{ item.machine }} · {{ item.engine }}</span>
      </button>
      <div class="observation-card__panes" :class="item.orientation">
        <button v-for="pane in item.panes" :key="pane.key" type="button" class="observation-pane" :data-switcher-tab="item.id" :data-switcher-pane="pane.sessionId" :data-testid="`observation-pane-${item.id}-${pane.key}`" :disabled="!pane.sessionId" :aria-label="`进入 ${item.name} ${pane.label || ''}窗格`" @mousedown.prevent @click="pane.sessionId && emit('pane', item.id, pane.sessionId)">
          <PaneStatusIndicator :status="pane.status" :label="pane.label || '终端'" />
          <span v-if="unit(pane.sessionId)?.agentSaid" class="observation-pane__said">{{ unit(pane.sessionId)?.agentSaid }}</span>
          <span class="observation-pane__tail"><template v-if="tail(pane.sessionId).length"><span v-for="(line, index) in tail(pane.sessionId)" :key="index">{{ line || ' ' }}</span></template><span v-else class="observation-pane__empty">{{ pane.status === 'disconnected' ? '终端未连接' : unit(pane.sessionId) ? '暂无可用输出' : '等待状态与输出同步…' }}</span></span>
          <span class="observation-pane__foot"><span v-if="unit(pane.sessionId)?.activityAt" :title="new Date(unit(pane.sessionId)!.activityAt!).toLocaleString()">活动 {{ new Date(unit(pane.sessionId)!.activityAt!).toLocaleTimeString() }}</span><span v-else>{{ unit(pane.sessionId) ? '最后输出' : '尚未同步' }}</span><span v-if="pane.sessionId">进入 ↗</span></span>
        </button>
      </div>
      <div class="observation-card__path" :title="item.cwd">{{ item.cwd }}</div>
    </article>
  </div>
</template>

<style scoped>
.observation-cards { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); align-items: start; gap: 10px; padding: 4px 6px 10px; }
.observation-card { min-width: 0; overflow: hidden; border: 1px solid hsl(var(--border)); border-radius: 7px; background: hsl(var(--card)); }
.observation-card.selected { border-color: hsl(var(--primary) / .65); }
.observation-card__heading { width: 100%; min-height: 44px; display: flex; gap: 8px; align-items: center; padding: 10px; text-align: left; border-bottom: 1px solid hsl(var(--border)); }
.observation-card__heading:hover, .observation-pane:hover { background: hsl(var(--accent) / .5); }
.observation-card__number { font-size: 11px; color: hsl(var(--muted-foreground)); }
.observation-card__heading strong { flex: 1; min-width: 0; font-weight: 500; font-size: 13px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.observation-card__heading small { font-size: 10px; color: hsl(var(--muted-foreground)); }
.observation-card__engine { font-size: 10px; max-width: 110px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: hsl(var(--muted-foreground)); }
.observation-card__panes { display: flex; flex-direction: column; }
.observation-card__panes.row { flex-direction: row; }
.observation-pane { display: flex; flex: 1; flex-direction: column; gap: 8px; min-width: 0; width: 100%; min-height: 148px; padding: 10px; text-align: left; background: hsl(var(--background) / .4); }
.observation-pane + .observation-pane { border-top: 1px solid hsl(var(--border)); }
.row .observation-pane + .observation-pane { border-top: 0; border-left: 1px solid hsl(var(--border)); }
.observation-pane:disabled { cursor: default; }
.observation-pane__tail { height: 8.5em; flex: 0 0 8.5em; display: -webkit-box; -webkit-box-orient: vertical; -webkit-line-clamp: 5; overflow: hidden; white-space: pre-wrap; overflow-wrap: anywhere; font: 11px/1.7 ui-monospace, monospace; }
.observation-pane__tail > span { display: block; }
.observation-pane__said { font-size: 11px; overflow-wrap: anywhere; }
.observation-pane__empty, .observation-pane__foot { color: hsl(var(--muted-foreground)); font-size: 10px; }
.observation-pane__foot { display: flex; justify-content: space-between; gap: 8px; }
.observation-card__path { padding: 7px 10px; font-size: 10px; color: hsl(var(--muted-foreground)); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; border-top: 1px solid hsl(var(--border)); }
button:focus-visible { outline: 2px solid hsl(var(--ring)); outline-offset: -2px; }
@media (max-width: 600px) { .observation-cards { grid-template-columns: 1fr; } .observation-card__engine { max-width: 86px; } .observation-pane__tail { font-size: 11px; } }
</style>
