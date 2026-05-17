<script setup lang="ts">
import { computed } from 'vue'
import { PanelPane } from '@ce/components/pane'
import { usePortalEvents } from '@ce/composables/layout/usePortalEvents'
import {
  Info,
  Terminal,
  Globe,
} from 'lucide-vue-next'

interface Category {
  id: string
  label: string
  icon: typeof Info
  description?: string
}

interface Props {
  activeCategory: string
}

const props = defineProps<Props>()

const bus = usePortalEvents()

const categories: Category[] = [
  { id: 'system-info', label: 'System',   icon: Info,     description: '端口、进程、版本' },
  { id: 'terminal',   label: 'Terminal',  icon: Terminal, description: 'Shell、缓冲区、会话' },
  { id: 'network',    label: 'Network',   icon: Globe,    description: '认证码、Internet 隧道' },
]

const activeId = computed(() => props.activeCategory)

function selectCategory(id: string) {
  bus?.emit('settings.category', { categoryId: id })
}
</script>

<template>
  <PanelPane
    title="设置"
    :searchable="false"
    class="h-full"
  >
    <template #item>
      <nav class="flex flex-col gap-0.5 p-1">
        <button
          v-for="cat in categories"
          :key="cat.id"
          class="category-item"
          :class="{ 'category-item--active': activeId === cat.id }"
          @click="selectCategory(cat.id)"
        >
          <component :is="cat.icon" class="size-4 shrink-0" />
          <div class="flex flex-col items-start min-w-0">
            <span class="text-xs font-medium truncate leading-tight">{{ cat.label }}</span>
            <span
              v-if="cat.description"
              class="text-[10px] text-muted-foreground truncate leading-tight"
            >{{ cat.description }}</span>
          </div>
        </button>
      </nav>
    </template>
  </PanelPane>
</template>

<style scoped>
.category-item {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  padding: 6px 8px;
  border-radius: 6px;
  text-align: left;
  color: hsl(var(--muted-foreground));
  transition: background-color 0.1s, color 0.1s;
}

.category-item:hover {
  background-color: hsl(var(--muted) / 0.6);
  color: hsl(var(--foreground));
}

.category-item--active {
  background-color: hsl(var(--primary) / 0.1);
  color: hsl(var(--primary));
}

.category-item--active:hover {
  background-color: hsl(var(--primary) / 0.15);
}
</style>
