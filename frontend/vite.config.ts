import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { fileURLToPath, URL } from 'node:url'

// DPF Portal Registry enables CE+Pro split:
// CE build: import only chat/workspace/settings/topic portals in main.ts
// Pro build: import all portals
// See: docs/topics/TH-0503-v2r and DPF-FINAL-SPEC §11

const devApiTarget = process.env.VITE_DEV_API_TARGET

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
      '@ce': fileURLToPath(new URL('../../deepwork/frontend/src', import.meta.url))
    }
  },
  server: {
    port: 9001,
    host: true,
    proxy: devApiTarget
      ? {
          '/api': {
            target: devApiTarget,
            changeOrigin: true,
          },
        }
      : undefined,
  },
  build: {
    outDir: './dist',
    emptyOutDir: true,
    sourcemap: false,
    rollupOptions: {
      output: {
        manualChunks: {
          'vue-vendor': ['vue', 'vue-router', 'pinia'],
          'ui-vendor': ['radix-vue', 'lucide-vue-next']
        }
      }
    }
  },
  esbuild: {
    logLevel: 'silent'
  }
})
