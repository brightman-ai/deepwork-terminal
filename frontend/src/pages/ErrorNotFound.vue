<template>
  <div class="flex h-screen items-center justify-center">
    <div class="text-center">
      <h1 class="text-6xl font-bold text-primary">404</h1>
      <p class="mt-4 text-xl text-muted-foreground">Page Not Found</p>
      <Button @click="goHome" class="mt-6">
        Go Home
      </Button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { useRouter } from 'vue-router'
import { Button } from '@ce/components/ui/button'

const router = useRouter()

const goHome = () => {
  router.push('/')
}
</script>
