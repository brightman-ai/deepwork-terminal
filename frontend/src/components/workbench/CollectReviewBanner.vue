<template>
  <div
    v-if="reviewers.length > 0"
    class="cr-strip"
    data-testid="collect-review-banner"
  >
    <strong class="cr-title">✨ 会审回执：</strong>
    <div class="cr-pills">
      <span
        v-for="r in readyReviewers"
        :key="r.id"
        class="cr-chip cr-chip-done"
        :title="r.summary || '评审结论已返回'"
      >
        <span class="cr-chip-mark">✓</span>{{ r.title }} 已返回
      </span>
      <span
        v-for="r in timedOutReviewers"
        :key="r.id"
        class="cr-chip cr-chip-timeout"
        :title="r.summary || '评审超时未返回'"
      >
        <span class="cr-chip-mark">⏱</span>{{ r.title }} 超时
        <button
          type="button"
          class="cr-retry-btn"
          :data-testid="`btn-retry-review-${r.id}`"
          title="重新派发该评审"
          @click="emit('retry', r.id)"
        >
          ↻ 重试
        </button>
      </span>
    </div>
    <button
      type="button"
      class="cr-collect-btn"
      data-testid="btn-collect-to-main"
      :disabled="readyReviewers.length === 0"
      title="将已返回的评审结论汇总填入当前会话输入框"
      @click="emit('collect', readyReviewers.slice())"
    >
      📥 汇总填入当前会话输入框
    </button>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'

export interface ReviewReceipt {
  id: string
  title: string
  status: 'done' | 'timeout'
  summary?: string
}

const props = withDefaults(
  defineProps<{
    reviewers?: ReviewReceipt[]
  }>(),
  {
    reviewers: () => [],
  },
)

const emit = defineEmits<{
  (e: 'collect', receipts: ReviewReceipt[]): void
  (e: 'retry', reviewerId: string): void
}>()

const readyReviewers = computed<ReviewReceipt[]>(() =>
  props.reviewers.filter((r) => r.status === 'done'),
)

const timedOutReviewers = computed<ReviewReceipt[]>(() =>
  props.reviewers.filter((r) => r.status === 'timeout'),
)
</script>

<style scoped>
.cr-strip {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 5px 10px;
  border: 1px solid rgba(180, 142, 255, 0.4);
  border-radius: 8px;
  background: rgba(180, 142, 255, 0.1);
  overflow: hidden;
}
.cr-title {
  color: #e6d7ff;
  font-size: 11.5px;
  white-space: nowrap;
  flex-shrink: 0;
}
.cr-pills {
  display: flex;
  align-items: center;
  gap: 6px;
  flex: 1;
  min-width: 0;
  overflow-x: auto;
  scrollbar-width: none;
}
.cr-chip {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 8px;
  border-radius: 999px;
  font-size: 11px;
  white-space: nowrap;
  flex-shrink: 0;
}
.cr-chip-done {
  color: #c9f3dd;
  background: rgba(140, 232, 181, 0.12);
  border: 1px solid rgba(140, 232, 181, 0.35);
}
.cr-chip-timeout {
  color: #ffe3bd;
  background: rgba(243, 184, 85, 0.12);
  border: 1px solid rgba(243, 184, 85, 0.35);
}
.cr-chip-mark {
  font-size: 10px;
}
.cr-retry-btn {
  margin-left: 2px;
  padding: 1px 6px;
  border-radius: 5px;
  border: 1px solid rgba(243, 184, 85, 0.45);
  background: transparent;
  color: #ffe3bd;
  font-size: 10.5px;
  cursor: pointer;
}
.cr-retry-btn:hover {
  background: rgba(243, 184, 85, 0.18);
  color: #fff;
}
.cr-collect-btn {
  flex-shrink: 0;
  height: 24px;
  padding: 0 10px;
  border-radius: 6px;
  border: 0;
  background: var(--cta-white, #f2f4f8);
  color: var(--cta-white-fg, #0d1017);
  font-size: 11px;
  font-weight: 600;
  cursor: pointer;
  white-space: nowrap;
}
.cr-collect-btn:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}
@media (max-width: 640px) {
  .cr-strip {
    flex-wrap: wrap;
  }
  .cr-collect-btn {
    margin-left: auto;
  }
}
</style>
