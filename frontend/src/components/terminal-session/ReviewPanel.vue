<script setup lang="ts">
/**
 * ReviewPanel — the drawer's 审核 panel (TOP TAB 4, read-only v1).
 *
 * Shows `git diff` for the workbench cwd's repository: the changed-file list (status +
 * path + +N/-N) with each file's unified diff, mirroring VS Code's Source Control glance.
 * Scope = the WHOLE working tree (staged + unstaged + untracked). Purpose: skim "what did
 * this agent change" without leaving the terminal.
 *
 * NON-goals (v1): no commit/push/stage/discard/comment — this is a viewer only.
 *
 * cwd is the drawer's EFFECTIVE pane working directory, OWNED by ResourceDrawer (same
 * follow/lock model FilesPanel consumes): in FOLLOW mode it tracks the live active pane;
 * once LOCKED it freezes. We re-fetch only when sessionId/cwd actually change.
 */
import { ref, computed, watch, onMounted } from 'vue'
import { RefreshCw, ChevronRight } from 'lucide-vue-next'
import { gitDiff, type GitDiffFile, type GitDiffResult } from '@terminal/api/files'
import { fuzzyMatch } from '@terminal/utils/fuzzyMatch'
import DrawerSearchBox from '@terminal/components/terminal-session/DrawerSearchBox.vue'

const props = defineProps<{ sessionId: string; cwd: string }>()

const loading = ref(false)
const result = ref<GitDiffResult | null>(null)
const expanded = ref<string>('') // path of the currently-open file (accordion, one at a time)
const query = ref('') // client-side filename/path filter over the changed-file list (S-3)

async function load(): Promise<void> {
  loading.value = true
  try {
    result.value = await gitDiff(props.sessionId, props.cwd)
  } finally {
    loading.value = false
  }
}

// Re-anchor whenever the target session OR the anchored cwd changes (a follow-mode pane
// switch or a lock/unlock) — R-5: 审核 follows the new cwd's repo. A plain expand stays put.
function reload(): void {
  expanded.value = ''
  query.value = ''
  void load()
}
watch(() => props.sessionId, reload)
watch(() => props.cwd, reload)
onMounted(load)

function toggle(path: string): void {
  expanded.value = expanded.value === path ? '' : path
}

const files = computed<GitDiffFile[]>(() => result.value?.files ?? [])

// 审核内搜 (S-3): fuzzyMatch over the repo-relative path (basename included). Client-side over
// the already-loaded diff — same sync model as 最近文件/历史输入; no backend, no debounce.
const filteredFiles = computed<GitDiffFile[]>(() => files.value.filter((f) => fuzzyMatch(query.value, f.path)))

// ── status letter → label + color chip ──
function statusMeta(s: string): { label: string; cls: string } {
  switch (s) {
    case 'M': return { label: 'M', cls: 'bg-amber-500/15 text-amber-600 dark:text-amber-400 border-amber-500/40' }
    case 'A': return { label: 'A', cls: 'bg-green-500/15 text-green-600 dark:text-green-400 border-green-500/40' }
    case 'D': return { label: 'D', cls: 'bg-red-500/15 text-red-600 dark:text-red-400 border-red-500/40' }
    case 'R': return { label: 'R', cls: 'bg-sky-500/15 text-sky-600 dark:text-sky-400 border-sky-500/40' }
    case '?': return { label: '?', cls: 'bg-muted text-muted-foreground border-border' }
    default: return { label: s || '·', cls: 'bg-muted text-muted-foreground border-border' }
  }
}

// Split a repo-relative path into a dimmed parent + a bold basename (VS-Code row idiom).
function baseName(path: string): string {
  const i = path.lastIndexOf('/')
  return i >= 0 ? path.slice(i + 1) : path
}
function parentDir(path: string): string {
  const i = path.lastIndexOf('/')
  return i >= 0 ? path.slice(0, i) : ''
}

// ── unified-diff line classification (for coloring) ──
// Cap the rendered line count so a huge (but byte-capped) diff can't jank the panel; the
// backend already clips each file's diff bytes, this just bounds DOM node count.
const MAX_DIFF_LINES = 2000
type DiffLine = { text: string; cls: string }
function diffLines(diff: string): DiffLine[] {
  if (!diff) return []
  const raw = diff.split('\n')
  const clipped = raw.length > MAX_DIFF_LINES
  const lines = clipped ? raw.slice(0, MAX_DIFF_LINES) : raw
  const out: DiffLine[] = lines.map((text) => ({ text, cls: lineClass(text) }))
  if (clipped) out.push({ text: `… (超 ${MAX_DIFF_LINES} 行，已折叠)`, cls: 'rp-ln-meta' })
  return out
}
function lineClass(line: string): string {
  if (line.startsWith('@@')) return 'rp-ln-hunk'
  if (
    line.startsWith('diff --git') || line.startsWith('index ') || line.startsWith('+++') ||
    line.startsWith('---') || line.startsWith('new file') || line.startsWith('deleted file') ||
    line.startsWith('rename ') || line.startsWith('similarity ') || line.startsWith('old mode') ||
    line.startsWith('new mode') || line.startsWith('Binary files') || line.startsWith('\\ No newline')
  ) return 'rp-ln-meta'
  if (line.startsWith('+')) return 'rp-ln-add'
  if (line.startsWith('-')) return 'rp-ln-del'
  return 'rp-ln-ctx'
}
</script>

<template>
  <div class="rp flex flex-col h-full bg-background text-foreground" data-testid="review-panel">
    <!-- header: summary + refresh -->
    <div class="shrink-0 flex items-center gap-2 border-b border-border bg-card px-3 py-2">
      <span class="text-xs font-medium text-foreground">审核</span>
      <span v-if="result && !result.notGit && !result.noCwd" class="text-[0.62rem] text-muted-foreground tabular-nums">
        {{ files.length }} 个文件改动
      </span>
      <button
        class="ml-auto p-1 rounded text-muted-foreground hover:text-foreground hover:bg-muted/50 transition-colors"
        type="button" title="刷新"
        data-testid="rp-refresh"
        :disabled="loading"
        @click="load"
      >
        <RefreshCw class="size-3.5" :class="{ 'animate-spin': loading }" />
      </button>
    </div>

    <!-- filename/path filter over the changed-file list (S-3) — same primitive as 文件/历史输入 -->
    <DrawerSearchBox
      v-if="files.length"
      v-model="query"
      placeholder="筛选改动文件…"
      testid="rp-search"
      class="shrink-0 border-b border-border/40 px-2 py-1.5"
    />

    <!-- truncated banner (too many changes / total budget spent) -->
    <div
      v-if="result?.truncated"
      class="mx-2 mt-1.5 rounded-md bg-amber-500/10 px-2 py-1.5 text-[0.62rem] leading-snug text-amber-600 dark:text-amber-400"
      data-testid="rp-truncated"
    >
      改动较多，部分文件的 diff 已截断。
    </div>

    <div class="flex-1 overflow-y-auto p-2">
      <!-- loading -->
      <div v-if="loading && !result" class="px-2 py-6 text-center text-xs text-muted-foreground italic" data-testid="rp-loading">加载中…</div>
      <!-- empty states (R-4) -->
      <div v-else-if="result?.noCwd" class="px-2 py-6 text-center text-xs text-muted-foreground italic" data-testid="rp-empty-nocwd">未获取到工作目录</div>
      <div v-else-if="result?.error" class="px-2 py-6 text-center text-xs text-muted-foreground italic" data-testid="rp-empty-error">
        加载失败
        <button class="ml-2 underline hover:text-foreground" type="button" @click="load">重试</button>
      </div>
      <div v-else-if="result?.notGit" class="px-2 py-6 text-center text-xs text-muted-foreground italic" data-testid="rp-empty-notgit">当前目录不是 git 仓库</div>
      <div v-else-if="!files.length" class="px-2 py-6 text-center text-xs text-muted-foreground italic" data-testid="rp-empty-clean">工作树无改动</div>
      <div v-else-if="!filteredFiles.length" class="px-2 py-6 text-center text-xs text-muted-foreground italic" data-testid="rp-empty-nomatch">无匹配文件</div>

      <!-- changed-file list (accordion) -->
      <ul v-else class="flex flex-col gap-1">
        <li
          v-for="f in filteredFiles"
          :key="f.path"
          class="rounded-md border border-border bg-card overflow-hidden"
          :data-testid="`rp-file-${f.path}`"
        >
          <button
            class="w-full flex items-center gap-2 px-2 py-1.5 text-left hover:bg-muted/40 transition-colors"
            type="button"
            :title="f.orig ? `${f.orig} → ${f.path}` : f.path"
            @click="toggle(f.path)"
          >
            <ChevronRight class="size-3.5 shrink-0 text-muted-foreground transition-transform" :class="{ 'rotate-90': expanded === f.path }" />
            <span
              class="shrink-0 inline-flex items-center justify-center w-5 h-4 rounded border text-[0.6rem] font-bold tabular-nums"
              :class="statusMeta(f.status).cls"
            >{{ statusMeta(f.status).label }}</span>
            <span class="min-w-0 flex-1 truncate text-xs">
              <span v-if="parentDir(f.path)" class="text-muted-foreground/60">{{ parentDir(f.path) }}/</span>
              <span class="text-foreground font-medium">{{ baseName(f.path) }}</span>
            </span>
            <span v-if="f.binary" class="shrink-0 text-[0.58rem] text-muted-foreground">二进制</span>
            <template v-else>
              <span v-if="f.added" class="shrink-0 text-[0.6rem] tabular-nums text-green-600 dark:text-green-400">+{{ f.added }}</span>
              <span v-if="f.deleted" class="shrink-0 text-[0.6rem] tabular-nums text-red-600 dark:text-red-400">-{{ f.deleted }}</span>
            </template>
          </button>

          <!-- expanded unified diff — its OWN horizontal scroll so long lines never widen the body -->
          <div v-if="expanded === f.path" class="border-t border-border/60" :data-testid="`rp-diff-${f.path}`">
            <div v-if="f.binary" class="px-3 py-3 text-[0.66rem] text-muted-foreground italic">二进制文件，无法显示差异</div>
            <div v-else-if="!f.diff" class="px-3 py-3 text-[0.66rem] text-muted-foreground italic">无差异内容</div>
            <div v-else class="rp-diff overflow-x-auto">
              <div style="min-width: max-content">
                <div
                  v-for="(ln, i) in diffLines(f.diff)"
                  :key="i"
                  class="rp-ln"
                  :class="ln.cls"
                >{{ ln.text || ' ' }}</div>
              </div>
            </div>
          </div>
        </li>
      </ul>
    </div>
  </div>
</template>

<style scoped>
.rp-diff {
  background: #120c1c;
  font-family: 'Cascadia Code', 'Fira Code', 'SF Mono', monospace;
  font-size: 0.66rem;
  line-height: 1.55;
}
.rp-ln {
  white-space: pre;
  padding: 0 10px;
}
.rp-ln-add { background: rgba(96, 216, 144, 0.12); color: #7fe0a8; }
.rp-ln-del { background: rgba(255, 128, 128, 0.10); color: #f0a0a0; }
.rp-ln-hunk { color: #6fb8ff; background: rgba(96, 160, 255, 0.06); }
.rp-ln-meta { color: #7a6a96; }
.rp-ln-ctx { color: #c4b4dc; }
</style>
