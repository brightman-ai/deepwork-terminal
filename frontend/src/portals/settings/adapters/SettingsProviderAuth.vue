<script setup lang="ts">
import { CanvasPane } from '@ce/components/pane'
import { Globe } from 'lucide-vue-next'

interface Props {
  providerAccountId?: string
}

withDefaults(defineProps<Props>(), {
  providerAccountId: '',
})
</script>

<template>
  <CanvasPane mode="display" class="h-full">
    <div class="flex flex-col items-center justify-center h-full gap-3 p-4 text-center">
      <Globe :size="32" class="text-muted-foreground/40" />
      <p class="text-xs text-muted-foreground">WebChat 登录在 CLI 版本中不可用。</p>
    </div>
  </CanvasPane>
</template>
