<script setup lang="ts">
/**
 * FilePreview — lightweight READ-ONLY format-aware viewer for the drawer's 文件 panel.
 *
 * Three render modes keyed off the filename extension:
 *   · markdown (.md)            → rich HTML via `marked`
 *   · code (.go/.toml/.ts/…)    → syntax-highlighted via highlight.js (lazy-loaded +
 *                                  curated language set, so the highlighter never bloats
 *                                  the initial bundle — it loads on the first code preview)
 *   · plain (everything else)   → monospace text
 *
 * A 换行 (soft-wrap) toggle covers code/plain so long lines either wrap (mobile default,
 * no horizontal scroll) or stay on one line for alignment-sensitive content. Content is
 * the user's own local files on their own machine, so marked HTML is rendered as-is.
 */
import { ref, computed, watch } from 'vue'
import { marked } from 'marked'
import { WrapText } from 'lucide-vue-next'
import type { LanguageFn } from 'highlight.js'

const props = defineProps<{ name: string; text: string }>()

// extension → highlight.js language id (toml/ini/env share the ini grammar).
const EXT_LANG: Record<string, string> = {
  go: 'go', ts: 'typescript', tsx: 'typescript', mts: 'typescript', cts: 'typescript',
  js: 'javascript', jsx: 'javascript', mjs: 'javascript', cjs: 'javascript', vue: 'xml',
  py: 'python', rs: 'rust', rb: 'ruby', java: 'java', kt: 'kotlin', swift: 'swift',
  c: 'c', h: 'c', cpp: 'cpp', cc: 'cpp', hpp: 'cpp', cs: 'csharp', php: 'php',
  json: 'json', yaml: 'yaml', yml: 'yaml', toml: 'ini', ini: 'ini', conf: 'ini', env: 'ini',
  sh: 'bash', bash: 'bash', zsh: 'bash', fish: 'bash',
  html: 'xml', xml: 'xml', svg: 'xml', css: 'css', scss: 'css', less: 'css',
  sql: 'sql', dockerfile: 'dockerfile', diff: 'diff', patch: 'diff', lua: 'lua', proto: 'protobuf',
}

const ext = computed(() => {
  const base = props.name.split('/').pop() || props.name
  if (base.toLowerCase() === 'dockerfile') return 'dockerfile'
  const parts = base.split('.')
  return parts.length > 1 ? (parts.pop() || '').toLowerCase() : ''
})
const lang = computed(() => EXT_LANG[ext.value] || '')
const kind = computed<'markdown' | 'code' | 'plain'>(() => {
  if (['md', 'markdown', 'mdx'].includes(ext.value)) return 'markdown'
  return lang.value ? 'code' : 'plain'
})

const wrap = ref(true)

const mdHtml = computed(() =>
  kind.value === 'markdown' ? (marked.parse(props.text, { async: false }) as string) : '',
)

function escapeHtml(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
}

// Lazy highlight.js: core + a curated language set, loaded once on the first code preview.
let hljsPromise: Promise<typeof import('highlight.js/lib/core').default> | null = null
function loadHljs() {
  if (!hljsPromise) {
    hljsPromise = (async () => {
      const core = (await import('highlight.js/lib/core')).default
      const reg = async (id: string, imp: Promise<{ default: LanguageFn }>) =>
        core.registerLanguage(id, (await imp).default)
      await Promise.all([
        reg('go', import('highlight.js/lib/languages/go')),
        reg('typescript', import('highlight.js/lib/languages/typescript')),
        reg('javascript', import('highlight.js/lib/languages/javascript')),
        reg('python', import('highlight.js/lib/languages/python')),
        reg('rust', import('highlight.js/lib/languages/rust')),
        reg('json', import('highlight.js/lib/languages/json')),
        reg('yaml', import('highlight.js/lib/languages/yaml')),
        reg('ini', import('highlight.js/lib/languages/ini')),
        reg('bash', import('highlight.js/lib/languages/bash')),
        reg('xml', import('highlight.js/lib/languages/xml')),
        reg('css', import('highlight.js/lib/languages/css')),
        reg('sql', import('highlight.js/lib/languages/sql')),
        reg('markdown', import('highlight.js/lib/languages/markdown')),
        reg('dockerfile', import('highlight.js/lib/languages/dockerfile')),
        reg('diff', import('highlight.js/lib/languages/diff')),
        reg('ruby', import('highlight.js/lib/languages/ruby')),
        reg('java', import('highlight.js/lib/languages/java')),
        reg('c', import('highlight.js/lib/languages/c')),
        reg('cpp', import('highlight.js/lib/languages/cpp')),
      ])
      return core
    })()
  }
  return hljsPromise
}

const codeHtml = ref('')
watch(
  [() => props.text, () => props.name],
  async () => {
    if (kind.value !== 'code') { codeHtml.value = ''; return }
    codeHtml.value = escapeHtml(props.text) // instant, unhighlighted — replaced once hljs loads
    const want = props.text
    try {
      const hljs = await loadHljs()
      if (props.text !== want) return // a newer preview superseded this one
      if (lang.value && hljs.getLanguage(lang.value)) {
        codeHtml.value = hljs.highlight(props.text, { language: lang.value, ignoreIllegals: true }).value
      }
    } catch { /* keep the escaped plain text */ }
  },
  { immediate: true },
)
</script>

<template>
  <div class="filepreview relative h-full overflow-auto" data-testid="file-preview">
    <button
      v-if="kind !== 'markdown'"
      class="fp-wrap-toggle"
      :class="{ 'is-on': wrap }"
      type="button"
      :title="wrap ? '关闭自动换行' : '自动换行'"
      data-testid="file-preview-wrap"
      @click="wrap = !wrap"
    >
      <WrapText class="size-3.5" />
    </button>

    <div v-if="kind === 'markdown'" class="fp-md" v-html="mdHtml" />
    <pre v-else class="fp-code hljs" :class="wrap ? 'fp-wrap' : 'fp-nowrap'"><code v-html="codeHtml" /></pre>
  </div>
</template>

<style scoped>
.filepreview { background: transparent; }

.fp-wrap-toggle {
  position: absolute; top: 6px; right: 8px; z-index: 2;
  display: inline-flex; align-items: center; justify-content: center;
  padding: 3px; border-radius: 6px;
  color: #6f5a90; background: rgba(22, 15, 34, 0.85);
  border: 1px solid #2e2050;
}
.fp-wrap-toggle.is-on { color: #c080ff; border-color: #3a2860; }

/* ── code ── */
.fp-code {
  margin: 0; padding: 12px 12px 28px;
  font-family: 'Cascadia Code', 'Fira Code', 'SF Mono', ui-monospace, monospace;
  font-size: 0.7rem; line-height: 1.55;
  color: #d8c4f0; background: transparent;
  tab-size: 4;
}
.fp-wrap { white-space: pre-wrap; word-break: break-word; overflow-wrap: anywhere; }
.fp-nowrap { white-space: pre; overflow-x: auto; }

/* highlight.js token palette — dark purple, aligned with the drawer's v6 surface. */
.fp-code :deep(.hljs-comment), .fp-code :deep(.hljs-quote) { color: #6f5a90; font-style: italic; }
.fp-code :deep(.hljs-keyword), .fp-code :deep(.hljs-selector-tag), .fp-code :deep(.hljs-literal),
.fp-code :deep(.hljs-section), .fp-code :deep(.hljs-doctag) { color: #c080ff; }
.fp-code :deep(.hljs-string), .fp-code :deep(.hljs-regexp), .fp-code :deep(.hljs-addition) { color: #7fd8a0; }
.fp-code :deep(.hljs-number), .fp-code :deep(.hljs-symbol), .fp-code :deep(.hljs-bullet) { color: #f0a560; }
.fp-code :deep(.hljs-title), .fp-code :deep(.hljs-title.function_), .fp-code :deep(.hljs-name) { color: #80c0ff; }
.fp-code :deep(.hljs-built_in), .fp-code :deep(.hljs-type), .fp-code :deep(.hljs-class .hljs-title) { color: #f0c860; }
.fp-code :deep(.hljs-attr), .fp-code :deep(.hljs-attribute), .fp-code :deep(.hljs-variable),
.fp-code :deep(.hljs-template-variable) { color: #c8a0ff; }
.fp-code :deep(.hljs-meta), .fp-code :deep(.hljs-comment .hljs-doctag) { color: #8a76aa; }
.fp-code :deep(.hljs-deletion) { color: #f08080; }
.fp-code :deep(.hljs-emphasis) { font-style: italic; }
.fp-code :deep(.hljs-strong) { font-weight: 700; }

/* ── markdown prose ── */
.fp-md {
  padding: 12px 14px 28px;
  font-size: 0.74rem; line-height: 1.6; color: #d8c4f0;
  word-break: break-word; overflow-wrap: anywhere;
}
.fp-md :deep(h1), .fp-md :deep(h2), .fp-md :deep(h3) { color: #f0e0ff; font-weight: 700; line-height: 1.3; margin: 0.9em 0 0.4em; }
.fp-md :deep(h1) { font-size: 1.15rem; border-bottom: 1px solid #2e2050; padding-bottom: 0.2em; }
.fp-md :deep(h2) { font-size: 1.05rem; }
.fp-md :deep(h3) { font-size: 0.95rem; }
.fp-md :deep(h4), .fp-md :deep(h5), .fp-md :deep(h6) { color: #c8b4e0; font-weight: 600; margin: 0.8em 0 0.3em; }
.fp-md :deep(p) { margin: 0.5em 0; }
.fp-md :deep(a) { color: #80c0ff; text-decoration: underline; }
.fp-md :deep(ul), .fp-md :deep(ol) { margin: 0.5em 0; padding-left: 1.4em; }
.fp-md :deep(li) { margin: 0.2em 0; }
.fp-md :deep(strong) { color: #f0e0ff; font-weight: 700; }
.fp-md :deep(em) { font-style: italic; }
.fp-md :deep(code) {
  font-family: 'Cascadia Code', 'Fira Code', 'SF Mono', ui-monospace, monospace;
  font-size: 0.92em; background: rgba(192, 128, 255, 0.12); color: #e0c8ff;
  padding: 0.08em 0.35em; border-radius: 4px;
}
.fp-md :deep(pre) {
  background: rgba(0, 0, 0, 0.3); border: 1px solid #241934; border-radius: 8px;
  padding: 10px 12px; overflow-x: auto; margin: 0.6em 0;
}
.fp-md :deep(pre code) { background: none; padding: 0; color: #d8c4f0; font-size: 0.7rem; line-height: 1.5; }
.fp-md :deep(blockquote) {
  border-left: 3px solid #4a2a7a; margin: 0.6em 0; padding: 0.1em 0 0.1em 0.8em; color: #b0a0c8;
}
.fp-md :deep(table) { border-collapse: collapse; margin: 0.6em 0; font-size: 0.92em; display: block; overflow-x: auto; }
.fp-md :deep(th), .fp-md :deep(td) { border: 1px solid #2e2050; padding: 4px 8px; text-align: left; }
.fp-md :deep(th) { background: rgba(192, 128, 255, 0.08); color: #e0d4f0; }
.fp-md :deep(hr) { border: none; border-top: 1px solid #2e2050; margin: 1em 0; }
.fp-md :deep(img) { max-width: 100%; border-radius: 6px; }
</style>
