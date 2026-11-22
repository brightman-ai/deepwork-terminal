<template>
  <div class="bw-root" data-testid="bot-workspace-view">
    <div v-if="!bot" class="bw-empty">
      请选择左侧的常驻幕僚以进入对话与分诊空间。
    </div>

    <template v-else>
      <!-- 顶部幕僚身份与线程栏 -->
      <div class="bw-hero-strip">
        <div class="bw-hero-left">
          <div class="bw-avatar">{{ bot.emoji || '🤖' }}</div>
          <div class="bw-hero-meta">
            <div class="bw-hero-title-row">
              <strong class="bw-hero-name">{{ bot.name }}</strong>
              <span class="bw-badge-bot">7×24 常驻幕僚</span>
              <span v-if="boundProfile" class="bw-hero-prof">
                {{ boundProfile.name }} ({{ boundProfile.model }})
              </span>
            </div>
            <div class="bw-hero-role">
              {{ bot.roleTitle }} — 只读巡检不改代码，发现问题可一键直达终端或另开独立分支处理
            </div>
          </div>
        </div>
        <div class="bw-hero-actions">
          <button
            type="button"
            class="bw-btn-accent"
            data-testid="btn-patrol-bot"
            :disabled="busy"
            @click="handlePatrol"
          >
            🔍 立即巡检真实状态
          </button>
          <button
            type="button"
            class="bw-btn-drawer"
            :class="{ active: rightPanelOpen }"
            data-testid="btn-toggle-bot-drawer"
            @click="rightPanelOpen = !rightPanelOpen"
          >
            ◨ {{ rightPanelOpen ? '收起看板' : `看板${pendingCount ? ` ●${pendingCount}` : ''}` }}
          </button>
        </div>
      </div>

      <!-- 线程切换条 -->
      <div class="bw-thread-bar" role="tablist">
        <button
          v-for="th in bot.threads"
          :key="th.id"
          type="button"
          class="bw-thread-tab"
          :class="{ active: activeThread?.id === th.id }"
          :data-testid="`bot-thread-tab-${th.id}`"
          @click="selectedThreadId = th.id"
        >
          {{ th.title }}
        </button>
      </div>

      <!-- 主体：左对话室 + 右上下文分诊看板 -->
      <div class="bw-body">
        <section class="bw-chat-col">
          <div class="bw-transcript">
            <template v-if="activeThread">
              <div
                v-for="msg in activeThread.messages"
                :key="msg.id"
                :class="msg.role === 'user' ? 'bw-msg-user' : 'bw-msg-bot'"
              >
                <div v-if="msg.role === 'bot'" class="bw-msg-head">
                  <strong>{{ bot.emoji || '🤖' }} {{ msg.meta || bot.name }}</strong>
                  <span>只读分诊留痕</span>
                </div>
                <div class="bw-msg-text">{{ msg.text }}</div>
                <div v-if="msg.files?.length" class="bw-msg-files">
                  <button
                    v-for="f in msg.files"
                    :key="f"
                    type="button"
                    class="bw-file-pill"
                    :title="f"
                    :data-testid="`bot-file-pill-${f}`"
                    @click="emitOpenFile(f)"
                  >
                    📄 {{ displayFileRef(f, bot.repoCwd) }}
                  </button>
                </div>
              </div>
            </template>

            <!-- 内嵌待拍板事项提示卡（主对话室） -->
            <div
              v-if="featuredPendingItem && activeThread?.kind === 'home'"
              class="bw-pending-banner"
              :class="{ spawned: Boolean(featuredPendingItem.spawnedWorktreePath) }"
            >
              <div class="bw-pending-head">
                <div class="bw-pending-meta">
                  <strong
                    :class="featuredPendingItem.spawnedWorktreePath ? 'bw-c-ok' : 'bw-c-wait'"
                  >
                    {{
                      featuredPendingItem.spawnedWorktreePath
                        ? `✓ 已新建独立分支目录 · #${featuredPendingItem.num}`
                        : `🔴 待你拍板 (#${featuredPendingItem.num} · 来自 ${featuredPendingItem.origin})`
                    }}
                  </strong>
                  <span
                    v-if="featuredPendingItem.repoCwd"
                    class="bw-repo-pill"
                    :title="featuredPendingItem.repoCwd"
                    :data-testid="`bot-banner-repo-${featuredPendingItem.id}`"
                  >
                    📁 {{ repoLabel(featuredPendingItem.repoCwd) }}
                  </span>
                </div>
                <div class="bw-pending-actions">
                  <button
                    v-if="featuredPendingItem.spawnedWorktreePath"
                    type="button"
                    class="bw-btn-primary-sm"
                    :data-testid="`bot-open-worktree-banner-${featuredPendingItem.id}`"
                    @click="
                      emit('open-worktree', {
                        path: featuredPendingItem.spawnedWorktreePath,
                        tabId: featuredPendingItem.spawnedTabId,
                      })
                    "
                  >
                    进入分支目录 ↗
                  </button>
                  <template v-else>
                    <button
                      v-if="
                        featuredPendingItem.sessionId ||
                        (isDirtyRepoItem(featuredPendingItem) && featuredPendingItem.repoCwd)
                      "
                      type="button"
                      class="bw-btn-white"
                      :data-testid="`bot-jump-session-banner-${featuredPendingItem.id}`"
                      @click="
                        emit('jump-session', {
                          sessionId: featuredPendingItem.sessionId || '',
                          cwd: featuredPendingItem.repoCwd,
                        })
                      "
                    >
                      {{
                        featuredPendingItem.sessionId && !isDirtyRepoItem(featuredPendingItem)
                          ? '↗ 前往会话处理'
                          : '↗ 进入该仓终端'
                      }}
                    </button>
                    <button
                      type="button"
                      class="bw-btn-dismiss"
                      :data-testid="`bot-dismiss-item-banner-${featuredPendingItem.id}`"
                      :disabled="busy"
                      @click="
                        emit('dismiss-item', {
                          botId: bot.id,
                          itemId: featuredPendingItem.id,
                        })
                      "
                    >
                      ✓ 已知晓
                    </button>
                    <button
                      v-if="featuredPendingItem.repoCwd || bot.repoCwd"
                      type="button"
                      class="bw-btn-spawn"
                      :data-testid="`bot-spawn-worktree-banner-${featuredPendingItem.id}`"
                      :disabled="spawningItemId === featuredPendingItem.id"
                      :title="
                        isDirtyRepoItem(featuredPendingItem)
                          ? '基于最新提交另开干净分支目录（不会搬移当前未提交文件）'
                          : '基于当前仓库新建独立分支目录并打开终端'
                      "
                      @click="
                        emit('spawn-worktree', {
                          botId: bot.id,
                          itemId: featuredPendingItem.id,
                        })
                      "
                    >
                      {{
                        spawningItemId === featuredPendingItem.id
                          ? '正在新建分支…'
                          : isDirtyRepoItem(featuredPendingItem)
                            ? '🌿 另开干净分支 ↗'
                            : '🌿 新建分支目录 ↗'
                      }}
                    </button>
                  </template>
                </div>
              </div>
              <div class="bw-pending-title">{{ featuredPendingItem.title }}</div>
              <div class="bw-pending-q">{{ featuredPendingItem.question }}</div>
              <div v-if="featuredPendingItem.targetFile" class="bw-msg-files">
                <button
                  type="button"
                  class="bw-file-pill"
                  :title="featuredPendingItem.targetFile"
                  :data-testid="`bot-banner-file-${featuredPendingItem.id}`"
                  @click="emitOpenFile(featuredPendingItem.targetFile, featuredPendingItem.repoCwd)"
                >
                  📄 {{ displayFileRef(featuredPendingItem.targetFile, featuredPendingItem.repoCwd || bot.repoCwd) }}
                </button>
              </div>
            </div>
          </div>

          <div v-if="errorMessage" class="bw-error-strip" role="alert">
            ⚠️ {{ errorMessage }}
          </div>

          <!-- 幕僚对话输入框 -->
          <div class="bw-composer">
            <div class="bw-quick-chips" data-testid="bot-quick-chips">
              <button
                type="button"
                class="bw-quick-chip"
                data-testid="bot-quick-chip-sessions"
                :disabled="busy"
                @click="handleQuickChat('汇总待处理会话')"
              >
                📊 汇总待处理会话
              </button>
              <button
                type="button"
                class="bw-quick-chip"
                data-testid="bot-quick-chip-dirty"
                :disabled="busy"
                @click="handleQuickChat('列出未提交变更仓库')"
              >
                📝 列出未提交变更仓库
              </button>
              <button
                type="button"
                class="bw-quick-chip"
                data-testid="bot-quick-chip-status"
                :disabled="busy"
                @click="handleQuickPrecheck('git status --short')"
              >
                🔍 git status --short
              </button>
            </div>
            <textarea
              v-model="chatDraft"
              class="bw-textarea"
              data-testid="bot-chat-input"
              placeholder="向常驻幕僚发问，或输入 /check git status --short 执行只读巡检…"
              @keydown.meta.enter.prevent="handleSendChat"
              @keydown.ctrl.enter.prevent="handleSendChat"
            />
            <div class="bw-composer-foot">
              <span class="bw-composer-hint">🛡️ 只读安全护栏生效 · ⌘↵ 发送</span>
              <button
                type="button"
                class="bw-btn-white"
                data-testid="bot-chat-send"
                :disabled="busy || !chatDraft.trim()"
                @click="handleSendChat"
              >
                ↑ 发送
              </button>
            </div>
          </div>
        </section>

        <!-- 右侧上下文看板：分诊看板 | 巡检门控 | 记忆 -->
        <aside
          v-show="rightPanelOpen"
          class="bw-right-panel"
          :class="{ 'is-mobile': isMobile }"
          data-testid="bot-right-panel"
        >
          <div class="bw-rp-head">
            <div class="bw-seg" role="tablist">
              <button
                type="button"
                class="bw-seg-btn"
                :aria-pressed="rightTab === 'items'"
                data-testid="bot-rtab-items"
                @click="rightTab = 'items'"
              >
                📋 分诊看板 ({{ bot.items.length }})
              </button>
              <button
                type="button"
                class="bw-seg-btn"
                :aria-pressed="rightTab === 'triggers'"
                data-testid="bot-rtab-triggers"
                @click="rightTab = 'triggers'"
              >
                ⚡ 自动巡检
              </button>
              <button
                type="button"
                class="bw-seg-btn"
                :aria-pressed="rightTab === 'soul'"
                data-testid="bot-rtab-soul"
                @click="rightTab = 'soul'"
              >
                🧠 记忆
              </button>
            </div>
            <button
              type="button"
              class="bw-icon-btn"
              data-testid="btn-close-bot-drawer"
              title="收起右栏"
              @click="rightPanelOpen = false"
            >
              ✕
            </button>
          </div>

          <div class="bw-rp-body">
            <!-- Tab 1: 分诊看板 -->
            <template v-if="rightTab === 'items'">
              <template v-for="grp in groupedItems" :key="grp.key">
                <div v-if="grp.items.length" class="bw-group-label">{{ grp.label }}</div>
                <article
                  v-for="it in grp.items"
                  :key="it.id"
                  class="bw-item-card"
                  :class="{ 'needs-you': it.status === 'needs_you' && !it.spawnedWorktreePath }"
                  :data-testid="`bot-item-card-${it.id}`"
                >
                  <div class="bw-item-top">
                    <div class="bw-item-top-left">
                      <span class="bw-item-num">#{{ it.num }}</span>
                      <span
                        v-if="it.repoCwd"
                        class="bw-repo-pill"
                        :title="it.repoCwd"
                        :data-testid="`bot-item-repo-${it.id}`"
                      >
                        📁 {{ repoLabel(it.repoCwd) }}
                      </span>
                    </div>
                    <span class="bw-origin-tag">{{ it.origin }}</span>
                  </div>
                  <strong class="bw-item-title">{{ it.title }}</strong>
                  <div class="bw-item-q">{{ it.question }}</div>
                  <div class="bw-item-foot">
                    <button
                      v-if="it.targetFile"
                      type="button"
                      class="bw-file-pill"
                      :title="it.targetFile"
                      :data-testid="`bot-item-file-${it.id}`"
                      @click="emitOpenFile(it.targetFile, it.repoCwd)"
                    >
                      📄 {{ displayFileRef(it.targetFile, it.repoCwd || bot.repoCwd) }}
                    </button>
                    <span v-else />
                    <div class="bw-item-actions">
                      <button
                        v-if="it.spawnedWorktreePath"
                        type="button"
                        class="bw-btn-primary-sm"
                        :data-testid="`bot-open-worktree-${it.id}`"
                        @click="
                          emit('open-worktree', {
                            path: it.spawnedWorktreePath,
                            tabId: it.spawnedTabId,
                          })
                        "
                      >
                        进入分支目录 ↗
                      </button>
                      <template v-else>
                        <button
                          v-if="it.sessionId || (isDirtyRepoItem(it) && it.repoCwd)"
                          type="button"
                          class="bw-btn-primary-sm"
                          :data-testid="`bot-jump-session-${it.id}`"
                          @click="
                            emit('jump-session', {
                              sessionId: it.sessionId || '',
                              cwd: it.repoCwd,
                            })
                          "
                        >
                          {{
                            it.sessionId && !isDirtyRepoItem(it)
                              ? '↗ 前往会话处理'
                              : '↗ 进入该仓终端'
                          }}
                        </button>
                        <template v-if="it.status === 'needs_you'">
                          <button
                            type="button"
                            class="bw-btn-dismiss"
                            :data-testid="`bot-dismiss-item-${it.id}`"
                            :disabled="busy"
                            @click="emit('dismiss-item', { botId: bot.id, itemId: it.id })"
                          >
                            ✓ 已知晓
                          </button>
                          <button
                            v-if="it.repoCwd || bot.repoCwd"
                            type="button"
                            class="bw-btn-spawn"
                            :data-testid="`bot-spawn-worktree-${it.id}`"
                            :disabled="spawningItemId === it.id"
                            :title="
                              isDirtyRepoItem(it)
                                ? '基于最新提交另开干净分支目录（不会搬移当前未提交文件）'
                                : '基于当前仓库新建独立分支目录并打开终端'
                            "
                            @click="emit('spawn-worktree', { botId: bot.id, itemId: it.id })"
                          >
                            {{
                              spawningItemId === it.id
                                ? '新建中…'
                                : isDirtyRepoItem(it)
                                  ? '🌿 另开干净分支 ↗'
                                  : '🌿 新建分支目录 ↗'
                            }}
                          </button>
                        </template>
                      </template>
                    </div>
                  </div>
                </article>
              </template>
              <div v-if="!bot.items.length" class="bw-empty-sm">
                当前无挂起事项，幕僚正静默值守中。
              </div>
            </template>

            <!-- Tab 2: 自动巡检 -->
            <template v-else-if="rightTab === 'triggers'">
              <div class="bw-guard-banner">
                <strong>🛡️ 只读安全护栏已开启</strong>：巡检命令仅允许读取状态与日志，自动拦截写文件、删除与代码提交。
              </div>

              <div
                v-for="tr in bot.triggers"
                :key="tr.id"
                class="bw-item-card"
                :data-testid="`bot-trigger-${tr.id}`"
              >
                <div class="bw-item-top">
                  <strong class="bw-item-title">{{ tr.name }}</strong>
                  <button
                    type="button"
                    class="bw-btn-ghost-xs"
                    :data-testid="`bot-run-trigger-${tr.id}`"
                    :disabled="busy"
                    @click="
                      emit('run-check', {
                        botId: bot.id,
                        triggerId: tr.id,
                        precheckCmd: tr.precheck,
                      })
                    "
                  >
                    ▶ 试跑预检
                  </button>
                </div>
                <div class="bw-precheck-line">
                  零 Token 预检：<code>{{ tr.precheck }}</code>
                </div>
                <div class="bw-trigger-stats">
                  <span>{{ tr.mode }}</span>
                  <span>{{ tr.stats || `零 Token 拦截 ${tr.skippedZeroToken} 次` }}</span>
                </div>
              </div>

              <div class="bw-item-card">
                <strong class="bw-item-title">临时只读命令验证</strong>
                <div class="bw-custom-check-row">
                  <input
                    v-model="customPrecheckCmd"
                    type="text"
                    class="bw-input-sm"
                    data-testid="bot-precheck-input"
                    placeholder="如 git status --short"
                  />
                  <button
                    type="button"
                    class="bw-btn-primary-sm"
                    data-testid="bot-run-precheck"
                    :disabled="busy || !customPrecheckCmd.trim()"
                    @click="handleRunCustomPrecheck"
                  >
                    运行
                  </button>
                </div>
              </div>
            </template>

            <!-- Tab 3: SOUL.md & MEMORY.md -->
            <template v-else>
              <div class="bw-docs-hint">
                纯本地 Markdown 存储于 <code>~/.dw-terminal/bots/{{ bot.slug }}/</code>（零平台锁定）：
              </div>
              <label class="bw-doc-label blue" for="bw-soul-ed">
                SOUL.md（职责与只读行为边界）
              </label>
              <textarea
                id="bw-soul-ed"
                v-model="soulDraft"
                class="bw-doc-textarea"
                data-testid="bot-soul-editor"
              />
              <label class="bw-doc-label green" for="bw-mem-ed">
                MEMORY.md（跨任务长期经验记忆）
              </label>
              <textarea
                id="bw-mem-ed"
                v-model="memoryDraft"
                class="bw-doc-textarea"
                data-testid="bot-memory-editor"
              />
              <div class="bw-docs-foot">
                <button
                  type="button"
                  class="bw-btn-primary-sm"
                  data-testid="btn-save-bot-docs"
                  :disabled="busy"
                  @click="handleSaveDocs"
                >
                  保存幕僚记忆
                </button>
              </div>
            </template>
          </div>
        </aside>
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { BotDetail, BotItem, BotThread } from '@terminal/api/bots'
import type { ProfileItem } from '@terminal/api/profiles'

const props = withDefaults(
  defineProps<{
    bot: BotDetail | null
    profiles?: ProfileItem[]
    busy?: boolean
    spawningItemId?: string | null
    errorMessage?: string | null
    isMobile?: boolean
  }>(),
  {
    profiles: () => [],
    busy: false,
    spawningItemId: null,
    errorMessage: null,
    isMobile: false,
  },
)

const emit = defineEmits<{
  (e: 'send-chat', payload: { botId: string; threadId: string; message: string }): void
  (e: 'save-docs', payload: { botId: string; soulMd: string; memoryMd: string }): void
  (
    e: 'run-check',
    payload: {
      botId: string
      triggerId?: string
      precheckCmd?: string
      patrol?: boolean
    },
  ): void
  (e: 'spawn-worktree', payload: { botId: string; itemId: string }): void
  (e: 'open-worktree', payload: { path?: string; tabId?: string }): void
  (e: 'open-file', payload: { path: string; line?: number; cwd?: string }): void
  (e: 'jump-session', payload: { sessionId: string; cwd?: string }): void
  (e: 'dismiss-item', payload: { botId: string; itemId: string }): void
}>()

const selectedThreadId = ref<string>('')
const rightPanelOpen = ref<boolean>(!props.isMobile)
const rightTab = ref<'items' | 'triggers' | 'soul'>('items')
const chatDraft = ref<string>('')
const customPrecheckCmd = ref<string>('git status --short')
const soulDraft = ref<string>('')
const memoryDraft = ref<string>('')

watch(
  () => props.bot,
  (next, prev) => {
    if (!next) return
    const switchedBot = !prev || prev.id !== next.id
    // F4: Preserve unsaved user edits in SOUL.md / MEMORY.md when the same bot refreshes
    // (e.g. after chat / run-check / worktree spawn). Only sync clean fields or bot switches.
    const soulDirty = !switchedBot && soulDraft.value !== prev.soulMd
    const memoryDirty = !switchedBot && memoryDraft.value !== prev.memoryMd
    if (switchedBot || !soulDirty) soulDraft.value = next.soulMd
    if (switchedBot || !memoryDirty) memoryDraft.value = next.memoryMd
    if (switchedBot || !next.threads.some((t) => t.id === selectedThreadId.value)) {
      selectedThreadId.value = next.threads[0]?.id ?? 'home'
    }
  },
  { immediate: true },
)

const activeThread = computed<BotThread | null>(() => {
  if (!props.bot) return null
  return (
    props.bot.threads.find((t) => t.id === selectedThreadId.value) ??
    props.bot.threads[0] ??
    null
  )
})

const boundProfile = computed<ProfileItem | null>(() => {
  if (!props.bot) return null
  return props.profiles.find((p) => p.id === props.bot?.profileId) ?? null
})

const pendingCount = computed<number>(() => {
  if (!props.bot) return 0
  return props.bot.items.filter(
    (it) => it.status === 'needs_you' && !it.spawnedWorktreePath,
  ).length
})

const featuredPendingItem = computed<BotItem | null>(() => {
  if (!props.bot) return null
  return props.bot.items.find((it) => it.status === 'needs_you') ?? null
})

const groupedItems = computed(() => {
  const items = props.bot?.items ?? []
  return [
    {
      key: 'needs_you',
      label: '🔴 待你拍板 (Needs you)',
      items: items.filter((i) => i.status === 'needs_you'),
    },
    {
      key: 'working',
      label: '◐ 自动排查中 (Working)',
      items: items.filter((i) => i.status === 'working'),
    },
    {
      key: 'done',
      label: '✓ 已闭环归档 (Closed)',
      items: items.filter((i) => i.status === 'done'),
    },
  ]
})

function isDirtyRepoItem(it: BotItem): boolean {
  return it.id.startsWith('live-dirty-')
}

function repoLabel(cwd?: string): string {
  if (!cwd) return ''
  const norm = cwd.replace(/\/+$/, '')
  const parts = norm.split('/').filter(Boolean)
  return parts[parts.length - 1] || norm
}

function displayFileRef(spec: string, cwd?: string): string {
  let out = spec.trim()
  const normCwd = (cwd || props.bot?.repoCwd || '').replace(/\/+$/, '')
  if (normCwd && out.startsWith(`${normCwd}/`)) {
    out = out.slice(normCwd.length + 1)
  }
  if (out.length > 52) {
    return `${out.slice(0, 24)}…${out.slice(-27)}`
  }
  return out
}

function emitOpenFile(spec: string, cwd?: string): void {
  const match = /^(.*):(\d+)$/.exec(spec.trim())
  if (match) {
    emit('open-file', { path: match[1], line: Number(match[2]), cwd })
  } else {
    emit('open-file', { path: spec.trim(), cwd })
  }
}

function handleQuickChat(message: string): void {
  if (!props.bot || props.busy) return
  emit('send-chat', {
    botId: props.bot.id,
    threadId: activeThread.value?.id || 'home',
    message,
  })
}

function handleQuickPrecheck(precheckCmd: string): void {
  if (!props.bot || props.busy) return
  emit('run-check', {
    botId: props.bot.id,
    precheckCmd,
  })
}

function handleSendChat(): void {
  if (!props.bot) return
  const msg = chatDraft.value.trim()
  if (!msg) return
  emit('send-chat', {
    botId: props.bot.id,
    threadId: activeThread.value?.id || 'home',
    message: msg,
  })
  chatDraft.value = ''
}

function handleSaveDocs(): void {
  if (!props.bot) return
  emit('save-docs', {
    botId: props.bot.id,
    soulMd: soulDraft.value,
    memoryMd: memoryDraft.value,
  })
}

function handlePatrol(): void {
  if (!props.bot) return
  emit('run-check', {
    botId: props.bot.id,
    patrol: true,
  })
}

function handleRunCustomPrecheck(): void {
  if (!props.bot) return
  const cmd = customPrecheckCmd.value.trim()
  if (!cmd) return
  emit('run-check', {
    botId: props.bot.id,
    precheckCmd: cmd,
  })
}
</script>

<style scoped>
.bw-root {
  flex: 1;
  min-width: 0;
  min-height: 0;
  height: 100%;
  display: flex;
  flex-direction: column;
  background: var(--bg-canvas, #0c0e12);
  color: var(--text, #eef1f6);
  overflow: hidden;
}
.bw-empty {
  flex: 1;
  display: grid;
  place-items: center;
  color: var(--text-muted, #6c7688);
  font-size: 13px;
}
.bw-hero-strip {
  padding: 10px 16px;
  background: linear-gradient(90deg, rgba(61, 214, 140, 0.09), rgba(110, 168, 254, 0.06));
  border-bottom: 1px solid var(--line, rgba(255, 255, 255, 0.075));
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  flex-wrap: wrap;
  flex-shrink: 0;
}
.bw-hero-left {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}
.bw-avatar {
  width: 32px;
  height: 32px;
  border-radius: 7px;
  background: #1b2824;
  border: 1px solid rgba(61, 214, 140, 0.4);
  display: grid;
  place-items: center;
  font-size: 16px;
  flex-shrink: 0;
}
.bw-hero-meta {
  min-width: 0;
}
.bw-hero-title-row {
  display: flex;
  align-items: center;
  gap: 7px;
  flex-wrap: wrap;
}
.bw-hero-name {
  font-size: 13.5px;
  color: #fff;
}
.bw-badge-bot {
  font-size: 10px;
  padding: 1px 6px;
  border-radius: 4px;
  border: 1px solid rgba(61, 214, 140, 0.45);
  color: #8ce8b5;
  background: rgba(61, 214, 140, 0.12);
}
.bw-hero-prof {
  font-size: 11px;
  color: var(--text-muted, #6c7688);
  font-family: var(--font-mono, ui-monospace, monospace);
}
.bw-hero-role {
  font-size: 11.5px;
  color: var(--text-secondary, #a2acc0);
  margin-top: 2px;
}
.bw-hero-actions {
  display: flex;
  align-items: center;
  gap: 6px;
}
.bw-btn-accent {
  height: 26px;
  padding: 0 9px;
  border-radius: 5px;
  border: 1px solid rgba(110, 168, 254, 0.42);
  background: var(--accent-bg, rgba(110, 168, 254, 0.15));
  color: #d6e6ff;
  font-size: 11px;
  font-weight: 500;
  white-space: nowrap;
  cursor: pointer;
}
.bw-btn-accent:hover:not(:disabled) {
  background: rgba(110, 168, 254, 0.24);
}
.bw-btn-drawer {
  height: 26px;
  padding: 0 8px;
  border-radius: 6px;
  border: 1px solid var(--line-strong, rgba(255, 255, 255, 0.145));
  background: var(--bg-surface, #161a22);
  color: var(--text-secondary, #a2acc0);
  font-size: 11.5px;
  cursor: pointer;
}
.bw-btn-drawer.active {
  border-color: rgba(110, 168, 254, 0.5);
  background: var(--accent-bg, rgba(110, 168, 254, 0.15));
  color: #fff;
}
.bw-thread-bar {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 6px 12px;
  background: var(--bg-sidebar, #11141a);
  border-bottom: 1px solid var(--line, rgba(255, 255, 255, 0.075));
  overflow-x: auto;
  flex-shrink: 0;
}
.bw-thread-tab {
  height: 25px;
  padding: 0 9px;
  border-radius: 5px;
  border: 1px solid transparent;
  background: rgba(255, 255, 255, 0.03);
  color: var(--text-secondary, #a2acc0);
  font-size: 11.5px;
  white-space: nowrap;
  cursor: pointer;
}
.bw-thread-tab.active {
  background: var(--bg-active, #253044);
  border-color: rgba(110, 168, 254, 0.44);
  color: #fff;
  font-weight: 500;
}
.bw-body {
  flex: 1;
  min-height: 0;
  display: flex;
  position: relative;
  overflow: hidden;
}
.bw-chat-col {
  flex: 1;
  min-width: 0;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.bw-transcript {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 14px 16px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.bw-msg-user {
  align-self: flex-end;
  max-width: 86%;
  background: #222938;
  border: 1px solid rgba(255, 255, 255, 0.1);
  padding: 8px 12px;
  border-radius: 12px 12px 3px 12px;
  font-size: 12.5px;
  line-height: 1.52;
  white-space: pre-wrap;
}
.bw-msg-bot {
  background: var(--bg-surface, #161a22);
  border: 1px solid var(--line, rgba(255, 255, 255, 0.075));
  border-radius: 9px;
  padding: 11px 14px;
  display: flex;
  flex-direction: column;
  gap: 7px;
  line-height: 1.58;
}
.bw-msg-head {
  display: flex;
  justify-content: space-between;
  font-size: 11px;
  color: var(--text-muted, #6c7688);
}
.bw-msg-text {
  white-space: pre-wrap;
  font-size: 12.5px;
}
.bw-msg-files {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
}
.bw-file-pill {
  background: var(--bg-canvas, #0c0e12);
  border: 1px solid var(--line-strong, rgba(255, 255, 255, 0.145));
  border-radius: 5px;
  padding: 2px 8px;
  font-family: var(--font-mono, ui-monospace, monospace);
  font-size: 11px;
  color: #9ec5fe;
  cursor: pointer;
}
.bw-file-pill:hover {
  border-color: #6ea8fe;
  color: #fff;
}
.bw-pending-banner {
  padding: 10px 12px;
  border-radius: 8px;
  background: #141822;
  border: 1px solid rgba(255, 107, 107, 0.5);
  display: flex;
  flex-direction: column;
  gap: 5px;
}
.bw-pending-banner.spawned {
  border-color: rgba(61, 214, 140, 0.45);
}
.bw-pending-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  flex-wrap: wrap;
}
.bw-pending-meta {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
  min-width: 0;
}
.bw-pending-actions,
.bw-item-actions {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  flex-wrap: wrap;
  margin-left: auto;
}
.bw-item-top-left {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  flex-wrap: wrap;
}
.bw-repo-pill {
  font-size: 10px;
  padding: 1px 6px;
  border-radius: 4px;
  background: rgba(110, 168, 254, 0.12);
  border: 1px solid rgba(110, 168, 254, 0.3);
  color: #b7d4ff;
  font-family: var(--font-mono, ui-monospace, monospace);
  max-width: 170px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.bw-btn-jump {
  background: rgba(110, 168, 254, 0.16);
  border: 1px solid rgba(110, 168, 254, 0.42);
  color: #d6e6ff;
  font-weight: 500;
  padding: 3px 8px;
  border-radius: 5px;
  font-size: 11px;
  cursor: pointer;
  white-space: nowrap;
}
.bw-btn-jump:hover {
  background: rgba(110, 168, 254, 0.26);
  color: #fff;
}
.bw-btn-dismiss {
  background: rgba(255, 255, 255, 0.05);
  border: 1px solid var(--line-strong, rgba(255, 255, 255, 0.145));
  color: var(--text-secondary, #a2acc0);
  padding: 3px 8px;
  border-radius: 5px;
  font-size: 11px;
  cursor: pointer;
  white-space: nowrap;
}
.bw-btn-dismiss:hover:not(:disabled) {
  background: rgba(255, 255, 255, 0.1);
  color: #fff;
}
.bw-btn-dismiss:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}
.bw-c-wait {
  color: #ffb8b8;
  font-size: 12px;
}
.bw-c-ok {
  color: #8ce8b5;
  font-size: 12px;
}
.bw-pending-title {
  font-size: 12.5px;
  font-weight: 600;
  color: var(--text, #eef1f6);
}
.bw-pending-q {
  font-size: 11.5px;
  color: var(--text-secondary, #a2acc0);
}
.bw-error-strip {
  margin: 0 14px 8px;
  padding: 6px 10px;
  border-radius: 6px;
  background: rgba(255, 107, 107, 0.14);
  border: 1px solid rgba(255, 107, 107, 0.45);
  color: #ffd0d0;
  font-size: 11.5px;
}
.bw-composer {
  margin: 0 14px 12px;
  background: #151924;
  border: 1px solid var(--line-strong, rgba(255, 255, 255, 0.145));
  border-radius: 10px;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  flex-shrink: 0;
}
.bw-composer:focus-within {
  border-color: rgba(110, 168, 254, 0.55);
}
.bw-quick-chips {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
  padding: 7px 10px 2px;
}
.bw-quick-chip {
  height: 22px;
  padding: 0 8px;
  border-radius: 999px;
  border: 1px solid var(--line-strong, rgba(255, 255, 255, 0.12));
  background: rgba(255, 255, 255, 0.04);
  color: var(--text-secondary, #a2acc0);
  font-size: 11px;
  cursor: pointer;
  white-space: nowrap;
}
.bw-quick-chip:hover:not(:disabled) {
  background: rgba(110, 168, 254, 0.14);
  border-color: rgba(110, 168, 254, 0.42);
  color: #eef4ff;
}
.bw-quick-chip:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}
.bw-textarea {
  width: 100%;
  min-height: 46px;
  max-height: 110px;
  resize: none;
  border: 0;
  background: transparent;
  padding: 9px 12px 4px;
  font-size: 12.5px;
  line-height: 1.5;
  color: var(--text, #eef1f6);
  outline: none;
}
.bw-composer-foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 5px 10px 7px;
  gap: 8px;
}
.bw-composer-hint {
  font-size: 11px;
  color: var(--text-muted, #6c7688);
}
.bw-btn-white {
  height: 27px;
  padding: 0 11px;
  border-radius: 6px;
  background: var(--cta-white, #f2f4f8);
  color: var(--cta-white-fg, #0d1017);
  border: 0;
  font-size: 11.5px;
  font-weight: 600;
  cursor: pointer;
  white-space: nowrap;
}
.bw-btn-white:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}
.bw-right-panel {
  width: 392px;
  max-width: 46%;
  background: var(--bg-sidebar, #11141a);
  border-left: 1px solid var(--line, rgba(255, 255, 255, 0.075));
  display: flex;
  flex-direction: column;
  flex-shrink: 0;
}
.bw-right-panel.is-mobile {
  position: absolute;
  inset: 0;
  width: 100%;
  max-width: 100%;
  border-left: 0;
  z-index: 20;
}
.bw-rp-head {
  height: 36px;
  padding: 0 8px 0 10px;
  background: var(--bg-surface, #161a22);
  border-bottom: 1px solid var(--line, rgba(255, 255, 255, 0.075));
  display: flex;
  align-items: center;
  gap: 6px;
  flex-shrink: 0;
}
.bw-seg {
  flex: 1;
  display: inline-flex;
  background: #0f1117;
  border: 1px solid var(--line, rgba(255, 255, 255, 0.075));
  padding: 2px;
  border-radius: 6px;
  gap: 2px;
}
.bw-seg-btn {
  flex: 1;
  color: var(--text-secondary, #a2acc0);
  padding: 2px 6px;
  border-radius: 4px;
  font-size: 11px;
  white-space: nowrap;
  background: transparent;
  border: 0;
  cursor: pointer;
}
.bw-seg-btn[aria-pressed='true'] {
  background: var(--bg-active, #253044);
  color: #fff;
  font-weight: 600;
}
.bw-icon-btn {
  width: 24px;
  height: 24px;
  border-radius: 5px;
  background: transparent;
  border: 0;
  color: var(--text-secondary, #a2acc0);
  cursor: pointer;
}
.bw-icon-btn:hover {
  background: var(--bg-hover, #222937);
  color: #fff;
}
.bw-rp-body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 10px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.bw-group-label {
  font-size: 11px;
  font-weight: 700;
  color: var(--text-secondary, #a2acc0);
  margin-top: 2px;
}
.bw-item-card {
  padding: 10px 11px;
  border-radius: 8px;
  background: var(--bg-canvas, #0c0e12);
  border: 1px solid var(--line-strong, rgba(255, 255, 255, 0.145));
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.bw-item-card.needs-you {
  border-color: rgba(255, 107, 107, 0.52);
  background: linear-gradient(180deg, rgba(255, 107, 107, 0.07), #0d1016);
}
.bw-item-top {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 6px;
}
.bw-item-num {
  font-family: var(--font-mono, ui-monospace, monospace);
  font-size: 10.5px;
  color: var(--text-muted, #6c7688);
}
.bw-origin-tag {
  font-size: 10px;
  padding: 1px 6px;
  border-radius: 4px;
  background: rgba(255, 255, 255, 0.05);
  border: 1px solid var(--line-strong, rgba(255, 255, 255, 0.145));
  color: var(--text-secondary, #a2acc0);
}
.bw-item-title {
  font-size: 12.5px;
  line-height: 1.4;
}
.bw-item-q {
  font-size: 11.5px;
  color: var(--text-secondary, #a2acc0);
  line-height: 1.45;
}
.bw-item-foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 6px;
  margin-top: 2px;
  flex-wrap: wrap;
}
.bw-btn-spawn {
  background: rgba(61, 214, 140, 0.13);
  border: 1px solid rgba(61, 214, 140, 0.4);
  color: #8ce8b5;
  font-weight: 500;
  padding: 3px 9px;
  border-radius: 5px;
  font-size: 11px;
  cursor: pointer;
  white-space: nowrap;
}
.bw-btn-spawn:hover:not(:disabled) {
  background: rgba(61, 214, 140, 0.22);
  color: #c8f7dd;
}
.bw-btn-spawn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
.bw-btn-primary-sm {
  background: #f2f4f8;
  color: #0d1017;
  font-weight: 600;
  padding: 4px 10px;
  border-radius: 5px;
  font-size: 11px;
  border: 0;
  cursor: pointer;
  white-space: nowrap;
}
.bw-btn-primary-sm:hover:not(:disabled) {
  background: #ffffff;
}
.bw-btn-ghost-xs {
  padding: 2px 7px;
  border-radius: 4px;
  font-size: 10.5px;
  background: #24293d;
  border: 1px solid #3c4363;
  color: #d4dcfa;
  cursor: pointer;
}
.bw-guard-banner {
  padding: 9px 11px;
  border-radius: 8px;
  background: rgba(61, 214, 140, 0.08);
  border: 1px solid rgba(61, 214, 140, 0.32);
  font-size: 11.5px;
  color: #ccf5de;
  line-height: 1.45;
}
.bw-precheck-line {
  font-size: 11px;
  color: var(--text-secondary, #a2acc0);
}
.bw-trigger-stats {
  display: flex;
  justify-content: space-between;
  font-size: 11px;
  color: #8ce8b5;
  font-family: var(--font-mono, ui-monospace, monospace);
}
.bw-custom-check-row {
  display: flex;
  gap: 6px;
}
.bw-input-sm {
  flex: 1;
  min-width: 0;
  height: 28px;
  padding: 0 8px;
  border-radius: 5px;
  background: var(--bg-surface, #161a22);
  border: 1px solid var(--line-strong, rgba(255, 255, 255, 0.145));
  color: var(--text, #eef1f6);
  font-size: 11.5px;
  font-family: var(--font-mono, ui-monospace, monospace);
}
.bw-docs-hint {
  font-size: 11px;
  color: var(--text-muted, #6c7688);
}
.bw-doc-label {
  font-size: 11px;
  font-weight: 600;
}
.bw-doc-label.blue {
  color: #9ec5fe;
}
.bw-doc-label.green {
  color: #8ce8b5;
}
.bw-doc-textarea {
  width: 100%;
  min-height: 110px;
  resize: vertical;
  background: #1c1f30;
  border: 1px solid #444869;
  border-radius: 7px;
  padding: 7px 10px;
  font-family: var(--font-mono, ui-monospace, monospace);
  font-size: 11.5px;
  line-height: 1.5;
  color: #f0f2ff;
}
.bw-docs-foot {
  display: flex;
  justify-content: flex-end;
}
.bw-empty-sm {
  padding: 18px;
  color: var(--text-muted, #6c7688);
  text-align: center;
  font-size: 12px;
}
</style>
