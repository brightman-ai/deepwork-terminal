<script setup lang="ts">
/**
 * DrawerSearchBox — the ONE search-input primitive shared by every drawer search/filter box
 * (最近文件 · 目录树 · 历史输入 · 审核). Owns ONLY the input chrome: a magnifier glyph, the
 * <input>, and a conditional clear-× — so all four boxes look, sit, and behave identically
 * (诉求2 / S-1). It deliberately owns nothing else: filtering (sync fuzzyMatch vs the tree's
 * async debounced /files/search), result rendering (five distinct item types), and row
 * actions all stay with each consumer, because they genuinely differ — forcing them in here
 * would be a leaky abstraction. The primitive just two-way-binds a query string.
 *
 * Layout note: the root is the inline `[icon][input][clear]` group only. The outer framing
 * (border-b + padding for a standalone row, or flex:1 inside a multi-control bar) is the
 * consumer's — passed via a fallthrough class that merges onto the root.
 *
 * type="text" (not "search") + inputmode="search": mirrors the FilesPanel baseline and avoids
 * WebKit's NATIVE clear-× duplicating our custom one; inputmode still gives mobile the search
 * keyboard. fuzzyMatch (the query consumer) is whitespace-robust, so no v-model.trim needed.
 */
import { Search, X } from 'lucide-vue-next'

defineProps<{
  modelValue: string
  placeholder?: string
  /** e2e id bound to the <input>; the clear button derives `${testid}-clear`. */
  testid?: string
}>()

defineEmits<{
  (e: 'update:modelValue', value: string): void
}>()
</script>

<template>
  <div class="flex min-w-0 items-center gap-1.5">
    <Search class="size-3.5 shrink-0 text-muted-foreground/70" />
    <input
      :value="modelValue"
      type="text"
      inputmode="search"
      :placeholder="placeholder"
      class="min-w-0 flex-1 bg-transparent text-xs text-foreground placeholder:text-muted-foreground/60 outline-none"
      :data-testid="testid"
      @input="$emit('update:modelValue', ($event.target as HTMLInputElement).value)"
    />
    <button
      v-if="modelValue"
      class="shrink-0 rounded p-0.5 text-muted-foreground hover:bg-muted/50 hover:text-foreground"
      type="button"
      title="清除"
      :data-testid="testid ? `${testid}-clear` : undefined"
      @click="$emit('update:modelValue', '')"
    ><X class="size-3.5" /></button>
  </div>
</template>
