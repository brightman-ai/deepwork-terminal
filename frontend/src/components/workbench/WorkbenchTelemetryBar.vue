<template>
  <footer class="dtb-bar" data-testid="desktop-telemetry-bar">
    <div class="dtb-group dtb-left">
      <div class="dtb-profiles-list">
        <button
          v-for="prof in visibleProfiles"
          :key="prof.id"
          type="button"
          class="dtb-item"
          :class="{ 'is-default': prof.id === defaultProfileId || prof.isDefault }"
          :data-testid="`telemetry-profile-${prof.id}`"
          title="点击管理多套账号与 API 网关配置"
          @click="emit('open-profiles', { tab: 'list', profileId: prof.id })"
        >
          <span :style="{ color: engineColor(prof.engine) }">{{ engineGlyph(prof.engine) }}</span>
          <span>{{ prof.name }}</span>
          <template v-if="quotaOf(prof.id) !== null">
            <span class="dtb-mini-bar">
              <i
                :style="{
                  width: `${quotaOf(prof.id)}%`,
                  background: (quotaOf(prof.id) ?? 0) > 40 ? '#8ab8ff' : '#f3b855',
                }"
              />
            </span>
            <span>{{ quotaOf(prof.id) }}%</span>
          </template>
          <span v-else class="dtb-model-tag">{{ prof.model || prof.engine }}</span>
        </button>
      </div>

      <button
        v-if="profiles.length > MAX_VISIBLE_PROFILES"
        type="button"
        class="dtb-item dtb-all-profiles"
        data-testid="telemetry-all-profiles"
        title="查看并管理全部账号与 API 网关配置"
        @click="emit('open-profiles', { tab: 'list' })"
      >
        📚 全部 {{ profiles.length }} 套配置
      </button>

      <button
        type="button"
        class="dtb-item dtb-add"
        data-testid="telemetry-add-profile"
        @click="emit('open-profiles', { tab: 'gateway' })"
      >
        ＋新增配置
      </button>
    </div>

    <div class="dtb-group dtb-right">
      <span class="dtb-host">
        ● {{ hostLabel }}{{ latencyMs !== null ? ` · ${latencyMs}ms` : '' }}
      </span>
      <button
        type="button"
        class="dtb-item"
        data-testid="telemetry-bots-btn"
        @click="emit('open-bots')"
      >
        🤖 {{ bots.length }} 常驻幕僚在线
      </button>
      <span>&gt;_ {{ paneCount }} 窗格</span>
      <button
        type="button"
        class="dtb-item"
        data-testid="telemetry-worktrees-btn"
        @click="emit('open-worktrees')"
      >
        ⎇ {{ managedWorktreeCount }} 分支目录
      </button>
      <span v-if="totalCacheMB > 0">💾 缓存 {{ totalCacheMB }}MB</span>
    </div>
  </footer>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { ProfileEngine, ProfileItem } from '@terminal/api/profiles'
import type { BotDetail } from '@terminal/api/bots'
import type { WorktreeInfo } from '@terminal/api/worktrees'

const MAX_VISIBLE_PROFILES = 3

const props = withDefaults(
  defineProps<{
    profiles: ProfileItem[]
    defaultProfileId?: string
    bots: BotDetail[]
    worktrees: WorktreeInfo[]
    paneCount?: number
    hostLabel?: string
    latencyMs?: number | null
    quotaByProfileId?: Record<string, { pct: number; resetIn?: string }>
  }>(),
  {
    defaultProfileId: '',
    paneCount: 1,
    hostLabel: 'local',
    latencyMs: null,
    quotaByProfileId: () => ({}),
  },
)

const emit = defineEmits<{
  (e: 'open-profiles', payload?: { tab?: 'list' | 'oauth' | 'gateway'; profileId?: string }): void
  (e: 'open-bots'): void
  (e: 'open-worktrees'): void
}>()

const visibleProfiles = computed<ProfileItem[]>(() => {
  const list = props.profiles
  let defaultIdx = -1
  if (props.defaultProfileId) {
    defaultIdx = list.findIndex((p) => p.id === props.defaultProfileId)
  }
  if (defaultIdx < 0) {
    defaultIdx = list.findIndex((p) => p.isDefault)
  }
  const ordered =
    defaultIdx > 0
      ? [list[defaultIdx]!, ...list.slice(0, defaultIdx), ...list.slice(defaultIdx + 1)]
      : list
  return ordered.slice(0, MAX_VISIBLE_PROFILES)
})

const managedWorktreeCount = computed(
  () => props.worktrees.filter((w) => !w.isPrimary).length,
)

const totalCacheMB = computed(() =>
  props.worktrees.reduce((sum, w) => sum + (w.cacheMB || 0), 0),
)

function quotaOf(profileId: string): number | null {
  const entry = props.quotaByProfileId[profileId]
  if (!entry || typeof entry.pct !== 'number') return null
  return Math.max(0, Math.min(100, Math.round(entry.pct)))
}

function engineGlyph(engine: ProfileEngine): string {
  if (engine === 'claude') return '✴'
  if (engine === 'codex') return '🌀'
  return '>_ '
}

function engineColor(engine: ProfileEngine): string {
  if (engine === 'claude') return '#e58e73'
  if (engine === 'codex') return '#7dd3fc'
  return '#a2acc0'
}
</script>

<style scoped>
.dtb-bar {
  height: 24px;
  background: #0a0c10;
  border-top: 1px solid var(--line, rgba(255, 255, 255, 0.075));
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 10px;
  font-size: 10.5px;
  color: var(--text-muted, #6c7688);
  flex-shrink: 0;
  user-select: none;
  gap: 8px;
}
.dtb-group {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  overflow: hidden;
  min-width: 0;
}
.dtb-left {
  flex: 1;
}
.dtb-profiles-list {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  overflow: hidden;
}
.dtb-right {
  flex-shrink: 0;
}
.dtb-all-profiles {
  flex-shrink: 0;
  background: rgba(255, 255, 255, 0.05);
  border: 1px solid var(--line, rgba(255, 255, 255, 0.09));
  border-radius: 999px;
  padding: 1px 7px;
  color: #c8d3e6;
}
.dtb-all-profiles:hover {
  background: rgba(255, 255, 255, 0.1);
}
.dtb-item {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  color: var(--text-secondary, #a2acc0);
  padding: 1px 5px;
  border-radius: 4px;
  background: transparent;
  border: 0;
  cursor: pointer;
  white-space: nowrap;
  font-size: 10.5px;
}
.dtb-item:hover {
  background: rgba(255, 255, 255, 0.06);
  color: #fff;
}
.dtb-item.is-default {
  color: #dce8ff;
}
.dtb-add {
  color: #6ea8fe;
  flex-shrink: 0;
}
.dtb-mini-bar {
  width: 34px;
  height: 4px;
  border-radius: 2px;
  background: rgba(255, 255, 255, 0.12);
  overflow: hidden;
}
.dtb-mini-bar > i {
  display: block;
  height: 100%;
  border-radius: 2px;
}
.dtb-model-tag {
  font-family: var(--font-mono, ui-monospace, monospace);
  font-size: 10px;
  color: var(--text-muted, #6c7688);
}
.dtb-host {
  color: #8ce8b5;
}
@media (max-width: 768px) {
  .dtb-bar {
    display: none;
  }
}
</style>
