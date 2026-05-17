<script setup lang="ts">
withDefaults(defineProps<{
  value?: number | string
  tone?: 'neutral' | 'good' | 'warning' | 'danger'
}>(), {
  value: '',
  tone: 'neutral',
})
</script>

<template>
  <span
    v-if="value !== '' && value !== undefined"
    class="counter-badge"
    :class="`counter-badge--${tone}`"
  >
    {{ value }}
  </span>
</template>

<style scoped>
.counter-badge {
  display: inline-flex;
  min-width: 1rem;
  height: 1rem;
  align-items: center;
  justify-content: center;
  border-radius: 999px;
  padding: 0 0.25rem;
  font-size: 0.625rem;
  line-height: 1;
  font-weight: 600;
  background: hsl(var(--muted));
  color: hsl(var(--muted-foreground));
}

.counter-badge--good {
  background: rgb(220 252 231);
  color: rgb(22 101 52);
}

.counter-badge--warning {
  background: rgb(254 249 195);
  color: rgb(133 77 14);
}

.counter-badge--danger {
  background: rgb(254 226 226);
  color: rgb(153 27 27);
}
</style>
