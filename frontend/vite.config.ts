import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { fileURLToPath, URL } from 'node:url'

// DPF Portal Registry enables CE+Pro split:
// CE build: import only chat/workspace/settings/topic portals in main.ts
// Pro build: import all portals
// See: docs/topics/TH-0503-v2r and DPF-FINAL-SPEC §11

const devApiTarget = process.env.VITE_DEV_API_TARGET

// This tree builds the standalone terminal, so its bundle must never come out as the pro edition.
// The portal set is a build-time constant (CE features.ts reads VITE_PORTALS, else VITE_EDITION,
// which defaults to pro), and a bare `vite build` inherited that pro default: d22b84c, c4bc8bc and
// 42ce144 each embedded the full pro nav on :18074. Defaulted here so build.sh, `bun run build` and
// CI all agree; an explicit VITE_PORTALS in the environment still wins. (Vite reads VITE_* from
// process.env after this file runs; pro builds through its own config and is unaffected.)
// [Ref: FMEA.md F13]
process.env.VITE_PORTALS ??= 'cli,settings'

// The pinned CE tree's bare imports must resolve to THIS install: Rollup
// walks up from the importing file and never reaches our node_modules from
// ../../deepwork (same physics that broke the type check — see the matching
// paths in tsconfig.json). One physical copy also keeps a single vue context
// in the bundle. Keep in sync with the tsconfig paths list.
const ceToolchain = [
  'vue',
  'vue-router',
  '@vueuse/core',
  'lucide-vue-next',
  'reka-ui',
  'clsx',
  'tailwind-merge',
  'class-variance-authority',
  '@radix-icons/vue'
]

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: {
      '@terminal': fileURLToPath(new URL('./src', import.meta.url)),
      '@ce': fileURLToPath(new URL('../../deepwork/frontend/src', import.meta.url)),
      ...Object.fromEntries(
        ceToolchain.map((pkg) => [pkg, fileURLToPath(new URL(`./node_modules/${pkg}`, import.meta.url))])
      ),
      // runtime wants the real package; the tsconfig maps it to @types for checking
      qrcode: fileURLToPath(new URL('./node_modules/qrcode', import.meta.url))
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
          'ui-vendor': ['reka-ui', 'lucide-vue-next']
        }
      }
    }
  },
  esbuild: {
    logLevel: 'silent'
  }
})
