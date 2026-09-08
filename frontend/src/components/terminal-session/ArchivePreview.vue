<script setup lang="ts">
/**
 * ArchivePreview — zip 的预览 = 它里面装了什么。
 *
 * **只列清单，不解压**：解压到临时目录会引出生命周期、清理、zip 炸弹三条新问题，而"这里面
 * 装了什么"这一个问题，中央目录就答完了（读它不需要解压任何字节）。想要内容 → 下载。
 */
interface ZipEntry { name: string; size: number; compressed: number; isDir: boolean }
const props = defineProps<{ name: string; entries: ZipEntry[]; truncated?: boolean }>()

function human(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1 << 20) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / (1 << 20)).toFixed(1)} MB`
}
/** 压缩率：给一眼"这包里是已压过的媒体还是可压的文本"。0 字节条目不显示（除不动）。 */
function ratio(e: ZipEntry): string {
  if (e.isDir || e.size <= 0) return ''
  return `${Math.round((1 - e.compressed / e.size) * 100)}%`
}
const files = props.entries.filter((e) => !e.isDir)
const totalRaw = files.reduce((a, e) => a + e.size, 0)
</script>

<template>
  <div class="flex h-full flex-col" data-testid="fp-preview-zip">
    <div class="flex-none border-b border-border px-3 py-2">
      <p class="text-[0.68rem] text-muted-foreground">
        {{ files.length }} 个文件 · 解压后共 {{ human(totalRaw) }}
        <span v-if="truncated"> · 条目过多，仅列出前面一部分</span>
      </p>
    </div>
    <div class="min-h-0 flex-1 overflow-auto px-1 py-1 zip-list">
      <div
        v-for="(e, i) in entries"
        :key="i"
        class="flex items-center gap-2 rounded px-2 py-1 text-[0.7rem] hover:bg-muted/40"
        :data-testid="`fp-zip-entry-${i}`"
      >
        <span class="shrink-0 text-muted-foreground">{{ e.isDir ? '📁' : '📄' }}</span>
        <span class="min-w-0 flex-1 truncate text-foreground" :title="e.name">{{ e.name }}</span>
        <span v-if="!e.isDir" class="shrink-0 tabular-nums text-muted-foreground">{{ human(e.size) }}</span>
        <span v-if="!e.isDir" class="w-10 shrink-0 text-right tabular-nums text-muted-foreground/60">{{ ratio(e) }}</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* 抽屉面板整体 user-select:none —— 条目路径要能复制（常用来拼命令）。 */
.zip-list { user-select: text; -webkit-user-select: text; }
</style>
