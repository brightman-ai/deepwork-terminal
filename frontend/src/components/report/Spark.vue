<script setup lang="ts">
// CHG-014 R3 §2.2 — shared spark (柱状趋势). v6 .spark idiom (v6:163-188 段).
// Honest empty state: no/empty bars → 「—」(never a fake flat line).
const props = withDefaults(defineProps<{ bars?: number[] }>(), { bars: () => [] })

// Normalise to 0..1 against the max so a single tall bar doesn't dwarf the rest.
function heights(): number[] {
  const bs = props.bars ?? []
  const max = Math.max(1, ...bs)
  return bs.map((b) => Math.max(0.08, b / max))
}
</script>

<template>
  <span v-if="bars && bars.length" class="rpt-spark" data-testid="report-spark">
    <i v-for="(h, i) in heights()" :key="i" :style="{ height: (h * 100).toFixed(0) + '%' }" />
  </span>
  <span v-else class="rpt-spark-empty">—</span>
</template>

<style scoped>
.rpt-spark {
  display: inline-flex;
  align-items: flex-end;
  gap: 1.5px;
  height: 16px;
  width: 48px;
  vertical-align: middle;
}
.rpt-spark i {
  flex: 1;
  min-height: 1px;
  border-radius: 1px;
  background: linear-gradient(180deg, var(--dw-ac), var(--dw-ac-dim, var(--dw-ac)));
  opacity: 0.85;
}
.rpt-spark-empty {
  font-family: var(--dw-mono);
  font-variant-numeric: tabular-nums;
  color: var(--dw-mu);
}
</style>
