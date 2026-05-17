<template>
  <div class="quick-bar">
    <!-- Normal mode — JumpDesktop-style keyboard key strip -->
    <template v-if="mode === 'normal'">
      <button class="qb-btn qb-btn--danger" @click="$emit('sendKey', '\x03')" title="Ctrl+C 中断">^C</button>
      <button class="qb-btn qb-btn--nav" @click="$emit('sendKey', '\x1b[A')" title="上 (历史)">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round"><polyline points="18,15 12,9 6,15"/></svg>
      </button>
      <button class="qb-btn qb-btn--nav" @click="$emit('sendKey', '\x1b[D')" title="左">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round"><polyline points="15,18 9,12 15,6"/></svg>
      </button>
      <button class="qb-btn qb-btn--nav" @click="$emit('sendKey', '\x1b[C')" title="右">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round"><polyline points="9,18 15,12 9,6"/></svg>
      </button>
      <button class="qb-btn" @click="$emit('sendKey', '\t')" title="Tab 补全">Tab</button>
      <button class="qb-btn" @click="$emit('sendKey', 'y\n')" title="y + 回车">y</button>
      <button class="qb-btn" @click="$emit('sendKey', 'n\n')" title="n + 回车">n</button>
      <div class="qb-divider" />
      <button class="qb-btn qb-btn--select" @click="$emit('enterSelection')" title="进入复制模式">
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
          <rect x="3" y="3" width="8" height="8"/><rect x="13" y="3" width="8" height="8"/>
          <rect x="3" y="13" width="8" height="8"/><rect x="13" y="13" width="8" height="8"/>
        </svg>
        <span>选择</span>
      </button>
      <button class="qb-btn qb-btn--compose" @click="$emit('toggleCompose')" title="打开输入框">
        <svg width="15" height="12" viewBox="0 0 20 14" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round">
          <rect x="1" y="1" width="18" height="12" rx="2"/>
          <line x1="4" y1="5" x2="6" y2="5"/><line x1="8" y1="5" x2="10" y2="5"/>
          <line x1="12" y1="5" x2="14" y2="5"/>
          <line x1="3" y1="9" x2="7" y2="9"/><line x1="9" y1="9" x2="15" y2="9"/>
        </svg>
      </button>
      <slot name="extra" />
    </template>

    <!-- Selection mode (xterm.js dual-anchor) — non-tmux -->
    <template v-else-if="mode === 'selection'">
      <button class="qb-btn qb-btn--danger" @click="$emit('sendKey', '\x03')" title="Ctrl+C">^C</button>
      <div class="qb-divider" />
      <button class="qb-btn qb-btn--nav qb-btn--scroll" @click="$emit('pageUp')" title="向上翻页">
        <svg width="12" height="8" viewBox="0 0 24 16" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round">
          <polyline points="18,10 12,4 6,10"/><polyline points="18,16 12,10 6,16"/>
        </svg>
      </button>
      <button class="qb-btn qb-btn--nav" @click="$emit('halfPageUp')" title="向上半页">
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" style="margin-bottom:-2px">
          <polyline points="18,15 12,9 6,15"/>
        </svg>
        <span style="font-size:0.62rem;line-height:1;margin-top:-1px">½</span>
      </button>
      <button class="qb-btn qb-btn--nav" @click="$emit('halfPageDown')" title="向下半页">
        <span style="font-size:0.62rem;line-height:1;margin-bottom:-1px">½</span>
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" style="margin-top:-2px">
          <polyline points="6,9 12,15 18,9"/>
        </svg>
      </button>
      <button class="qb-btn qb-btn--nav qb-btn--scroll" @click="$emit('pageDown')" title="向下翻页">
        <svg width="12" height="8" viewBox="0 0 24 16" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round">
          <polyline points="6,0 12,6 18,0"/><polyline points="6,6 12,12 18,6"/>
        </svg>
      </button>
      <div class="qb-divider" />
      <button class="qb-btn qb-btn--action" @click="$emit('selectAll')" title="全选当前屏">
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
          <rect x="3" y="3" width="7" height="7"/><rect x="14" y="3" width="7" height="7"/>
          <rect x="3" y="14" width="7" height="7"/><rect x="14" y="14" width="7" height="7"/>
        </svg>
        <span>全选</span>
      </button>
      <button class="qb-btn qb-btn--copy" @click="$emit('copySelection')" title="复制选中内容">
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
          <rect x="9" y="9" width="13" height="13" rx="2"/>
          <path d="M5 15H4a2 2 0 01-2-2V4a2 2 0 012-2h9a2 2 0 012 2v1"/>
        </svg>
        <span>复制</span>
      </button>
      <button class="qb-btn qb-btn--danger" @click="$emit('cancelSelection')" title="退出复制模式">
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round">
          <line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/>
        </svg>
        <span>退出</span>
      </button>
    </template>

    <!-- tmux-selection mode — unified tmux copy mode (scroll + select + copy) -->
    <template v-else-if="mode === 'tmux-selection'">
      <button class="qb-btn qb-btn--danger" @click="$emit('sendKey', '\x03')" title="Ctrl+C">^C</button>
      <span class="qb-mode-label">tmux</span>
      <div class="qb-divider" />
      <!-- Arrow keys for tmux cursor movement -->
      <button class="qb-btn qb-btn--nav" @click="$emit('sendKey', '\x1b[A')" title="↑ 上移">
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round"><polyline points="18,15 12,9 6,15"/></svg>
      </button>
      <button class="qb-btn qb-btn--nav" @click="$emit('sendKey', '\x1b[B')" title="↓ 下移">
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round"><polyline points="6,9 12,15 18,9"/></svg>
      </button>
      <button class="qb-btn qb-btn--nav" @click="$emit('sendKey', '\x1b[D')" title="← 左移">
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round"><polyline points="15,18 9,12 15,6"/></svg>
      </button>
      <button class="qb-btn qb-btn--nav" @click="$emit('sendKey', '\x1b[C')" title="→ 右移">
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round"><polyline points="9,18 15,12 9,6"/></svg>
      </button>
      <div class="qb-divider" />
      <!-- Page scroll -->
      <button class="qb-btn qb-btn--nav qb-btn--scroll" @click="$emit('pageUp')" title="上翻一页">
        <svg width="12" height="8" viewBox="0 0 24 16" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round">
          <polyline points="18,10 12,4 6,10"/><polyline points="18,16 12,10 6,16"/>
        </svg>
      </button>
      <button class="qb-btn qb-btn--nav qb-btn--scroll" @click="$emit('pageDown')" title="下翻一页">
        <svg width="12" height="8" viewBox="0 0 24 16" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round">
          <polyline points="6,0 12,6 18,0"/><polyline points="6,6 12,12 18,6"/>
        </svg>
      </button>
      <div class="qb-divider" />
      <!-- Space = start tmux selection -->
      <button class="qb-btn qb-btn--action" @click="$emit('tmuxStartSelect')" title="Space — 开始选择">
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
          <rect x="4" y="4" width="16" height="16" rx="2"/>
          <path d="M9 12h6"/>
        </svg>
        <span>选择</span>
      </button>
      <!-- Copy = Enter (copy to tmux buffer) + exit -->
      <button class="qb-btn qb-btn--copy" @click="$emit('tmuxCopy')" title="复制选中内容 (Enter)">
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
          <rect x="9" y="9" width="13" height="13" rx="2"/>
          <path d="M5 15H4a2 2 0 01-2-2V4a2 2 0 012-2h9a2 2 0 012 2v1"/>
        </svg>
        <span>复制</span>
      </button>
      <!-- Exit tmux copy mode -->
      <button class="qb-btn qb-btn--danger" @click="$emit('exitTmuxSelection')" title="退出 (q)">
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round">
          <line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/>
        </svg>
        <span>退出</span>
      </button>
    </template>
  </div>
</template>

<script setup lang="ts">
defineProps<{
  mode: 'normal' | 'selection' | 'tmux-selection'
}>()

defineEmits<{
  (e: 'sendKey', key: string): void
  (e: 'toggleCompose'): void
  (e: 'enterSelection'): void
  (e: 'cancelSelection'): void
  (e: 'copySelection'): void
  (e: 'selectAll'): void
  (e: 'pageUp'): void
  (e: 'pageDown'): void
  (e: 'halfPageUp'): void
  (e: 'halfPageDown'): void
  (e: 'tmuxStartSelect'): void
  (e: 'tmuxCopy'): void
  (e: 'exitTmuxSelection'): void
}>()
</script>

<style scoped>
.quick-bar {
  --qb-bg: #1c1c1e;
  --qb-btn-bg: #2c2c2e;
  --qb-btn-border: #3a3a3c;
  --qb-btn-depth: #111;
  --qb-btn-active: #3a3a3c;
  --qb-btn-color: #e8e8ea;
  --qb-divider: #3a3a3c;
}
@media (prefers-color-scheme: light) {
  .quick-bar {
    --qb-bg: #d8d8da;
    --qb-btn-bg: #e8e8ea;
    --qb-btn-border: #c0c0c2;
    --qb-btn-depth: #aaaaac;
    --qb-btn-active: #d0d0d2;
    --qb-btn-color: #1c1c1e;
    --qb-divider: #c0c0c2;
  }
}
.quick-bar {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 5px 8px 7px;
  background: var(--qb-bg);
  border-top: 1px solid var(--qb-btn-border);
  overflow-x: auto;
  overflow-y: hidden;
  -webkit-overflow-scrolling: touch;
  touch-action: pan-x;
  scrollbar-width: none;
}
.quick-bar::-webkit-scrollbar { display: none; }

.qb-mode-label {
  flex-shrink: 0;
  font-size: 0.68rem;
  font-weight: 600;
  color: #9b59b6;
  letter-spacing: 0.5px;
  text-transform: uppercase;
  padding: 0 4px;
}

.qb-btn {
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 3px;
  min-width: 40px;
  height: 40px;
  padding: 0 11px;
  background: var(--qb-btn-bg);
  color: var(--qb-btn-color);
  border: 1px solid var(--qb-btn-border);
  border-bottom: 2px solid var(--qb-btn-depth);
  border-radius: 6px;
  font-size: 0.82rem;
  font-weight: 500;
  cursor: pointer;
  touch-action: manipulation;
  user-select: none;
  -webkit-user-select: none;
  white-space: nowrap;
  transition: background 0.08s, transform 0.08s;
}
.qb-btn:active {
  background: var(--qb-btn-active);
  transform: translateY(1px) scale(0.96);
  border-bottom-width: 1px;
}
.qb-btn span { font-size: 0.72rem; }

.qb-divider {
  flex-shrink: 0;
  width: 1px;
  height: 24px;
  background: var(--qb-divider);
  margin: 0 3px;
  opacity: 0.6;
}

.qb-btn--nav {
  flex-direction: column;
  color: #8ec5ff;
  border-color: #2a4a6a;
  border-bottom-color: #111;
  background: #1a2d44;
  min-width: 36px;
  padding: 0 6px;
}
.qb-btn--nav:active { background: #1e3550; }
.qb-btn--scroll { min-width: 38px; }

.qb-btn--danger {
  color: #ff6b6b;
  border-color: #5a2020;
  border-bottom-color: #111;
  background: #2a1010;
  min-width: 40px;
}
.qb-btn--danger:active { background: #3a1818; }

.qb-btn--select {
  color: #ffd080;
  border-color: #4a3a10;
  border-bottom-color: #111;
  background: #221a08;
  min-width: 56px;
}
.qb-btn--select:active { background: #2e2410; }

.qb-btn--compose {
  color: #80b8ff;
  border-color: #1a3a5a;
  border-bottom-color: #111;
  background: #0e2030;
  min-width: 44px;
}
.qb-btn--compose:active { background: #162a40; }

.qb-btn--copy {
  color: #60d890;
  border-color: #104828;
  border-bottom-color: #111;
  background: #082018;
  min-width: 60px;
}
.qb-btn--copy:active { background: #0e2e20; }

.qb-btn--action {
  color: #80e8e8;
  border-color: #1a4a4a;
  border-bottom-color: #111;
  background: #0a2020;
  min-width: 56px;
}
.qb-btn--action:active { background: #102c2c; }
</style>
