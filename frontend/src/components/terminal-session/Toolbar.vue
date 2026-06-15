<template>
  <div class="toolbar-strip" @mousedown.prevent>
    <!-- 1. ESC -->
    <button class="tb-btn" @click="$emit('sendKey', '\x1b')" title="Escape">
      Esc
    </button>

    <!-- 2. TAB (icon-only to save width; title tooltip + the standard ⇥ glyph keep it clear) -->
    <button class="tb-btn tb-btn--tab" @click="$emit('sendKey', '\t')" title="Tab">
      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <polyline points="4,7 10,12 4,17" />
        <line x1="10" y1="12" x2="20" y2="12" />
        <line x1="20" y1="7" x2="20" y2="17" />
      </svg>
    </button>

    <!-- 3. Numpad toggle -->
    <button
      class="tb-btn"
      :class="{ 'tb-btn--panel-numpad': activePanel === 'numpad' }"
      @click="$emit('toggleNumpad')"
      title="Numpad"
    >
      <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
        <circle cx="5" cy="5" r="2" />
        <circle cx="12" cy="5" r="2" />
        <circle cx="19" cy="5" r="2" />
        <circle cx="5" cy="12" r="2" />
        <circle cx="12" cy="12" r="2" />
        <circle cx="19" cy="12" r="2" />
        <circle cx="5" cy="19" r="2" />
        <circle cx="12" cy="19" r="2" />
        <circle cx="19" cy="19" r="2" />
      </svg>
    </button>

    <!-- 4. Chat / Compose toggle -->
    <button
      class="tb-btn"
      :class="{ 'tb-btn--panel-compose': activePanel === 'compose' }"
      @click="$emit('toggleCompose')"
      title="Compose"
    >
      <svg width="15" height="14" viewBox="0 0 24 22" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
        <path d="M21 11.5a8.38 8.38 0 01-.9 3.8 8.5 8.5 0 01-7.6 4.7 8.38 8.38 0 01-3.8-.9L3 21l1.9-5.7a8.38 8.38 0 01-.9-3.8 8.5 8.5 0 014.7-7.6 8.38 8.38 0 013.8-.9h.5a8.48 8.48 0 018 8v.5z" />
      </svg>
    </button>

    <!-- 5. Shift (sticky modifier) -->
    <button
      class="tb-btn"
      :class="{ 'tb-btn--sticky-active': stickyShift }"
      @click="$emit('toggleShift')"
      title="Shift"
    >
      <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round">
        <path d="M12 3l-8 9h5v8h6v-8h5z" />
      </svg>
    </button>

    <!-- 6. Ctrl (sticky modifier) -->
    <button
      class="tb-btn"
      :class="{ 'tb-btn--sticky-active': stickyCtrl }"
      @click="$emit('toggleCtrl')"
      title="Ctrl"
    >
      Ctrl
    </button>

    <!-- 7. Alt (sticky modifier) -->
    <button
      class="tb-btn"
      :class="{ 'tb-btn--sticky-active': stickyAlt }"
      @click="$emit('toggleAlt')"
      title="Alt"
    >
      Alt
    </button>

    <!-- 8. Paste — pastes the OS clipboard into the terminal via the robust
         paste path (handles iOS / HTTP fallback + HUD errors in the host). -->
    <button
      class="tb-btn tb-btn--paste"
      @click="$emit('clipboard', 'paste')"
      title="Paste from clipboard"
    >
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <rect x="8" y="2" width="8" height="4" rx="1" />
        <path d="M16 4h2a2 2 0 012 2v14a2 2 0 01-2 2H6a2 2 0 01-2-2V6a2 2 0 012-2h2" />
      </svg>
    </button>

    <!-- 9. Toggle system keyboard -->
    <button
      class="tb-btn"
      :class="{ 'tb-btn--active-blue': activePanel === 'none' && keyboardUp }"
      @click="$emit('toggleKeyboard')"
      title="Toggle keyboard"
    >
      <svg width="16" height="13" viewBox="0 0 24 18" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round">
        <rect x="1" y="1" width="22" height="16" rx="3"/>
        <line x1="5" y1="6" x2="7" y2="6"/><line x1="10" y1="6" x2="12" y2="6"/><line x1="15" y1="6" x2="17" y2="6"/>
        <line x1="5" y1="10" x2="7" y2="10"/><line x1="10" y1="10" x2="12" y2="10"/><line x1="15" y1="10" x2="17" y2="10"/>
        <line x1="8" y1="14" x2="16" y2="14"/>
      </svg>
    </button>

    <!-- 9.5. Attach file (📎) — mobile + remote upload -->
    <button
      class="tb-btn tb-btn--attach"
      @click="$emit('attach')"
      title="Upload file/image"
    >
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <path d="M21.44 11.05l-9.19 9.19a6 6 0 01-8.49-8.49l9.19-9.19a4 4 0 015.66 5.66l-9.2 9.19a2 2 0 01-2.83-2.83l8.49-8.48"/>
      </svg>
    </button>

    <!-- 10. Enter (frequently used, kept in page 1) -->
    <button class="tb-btn tb-btn--enter" @click="$emit('sendKey', '\r')" title="Enter">
      <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
        <polyline points="9,10 4,15 9,20"/><path d="M20 4v7a4 4 0 01-4 4H4"/>
      </svg>
    </button>

    <!-- Page 2: quick-access keys (scroll right to see) -->
    <div class="tb-divider" />
    <!-- Backspace (delete-left, \x7f) — needed when tmux is absent and the TmuxQuickBar
         ⌫ is hidden. Distinct from Del below, which is forward-delete (\x1b[3~). -->
    <button class="tb-btn tb-btn--extra tb-btn--bksp" @click="$emit('sendKey', '\x7f')" title="Backspace">⌫</button>
    <button class="tb-btn tb-btn--extra tb-btn--del" @click="$emit('sendKey', '\x1b[3~')" title="Delete (forward)">Del</button>
    <button class="tb-btn tb-btn--extra" @click="$emit('sendKey', '\x1b[5~')" title="Page Up">PgU</button>
    <button class="tb-btn tb-btn--extra" @click="$emit('sendKey', '\x1b[6~')" title="Page Down">PgD</button>
    <button class="tb-btn tb-btn--extra" @click="$emit('sendKey', '\x1b[A')" title="Arrow Up">↑</button>
    <button class="tb-btn tb-btn--extra" @click="$emit('sendKey', '\x1b[B')" title="Arrow Down">↓</button>
    <button class="tb-btn tb-btn--extra" @click="$emit('sendKey', '\x03')" title="Ctrl+C">^C</button>
    <button class="tb-btn tb-btn--extra" @click="$emit('sendKey', ' ')" title="Space (tmux select)">Spc</button>
    <!-- KeyCastr keystroke-display toggle (2nd-to-last). Reflects current on/off state;
         emits toggle-keycast so the surface flips its keystrokeHudVisible (default ON). -->
    <button
      class="tb-btn tb-btn--extra tb-btn--keycast"
      :class="{ 'tb-btn--keycast-on': keycastOn }"
      @click="$emit('toggleKeycast')"
      :title="keycastOn ? 'KeyCastr: on' : 'KeyCastr: off'"
    >
      <svg width="16" height="13" viewBox="0 0 24 18" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round">
        <rect x="1" y="1" width="22" height="16" rx="3"/>
        <line x1="5" y1="6" x2="6" y2="6"/><line x1="11" y1="6" x2="13" y2="6"/><line x1="18" y1="6" x2="19" y2="6"/>
        <line x1="6" y1="13" x2="18" y2="13"/>
      </svg>
    </button>
    <button
      class="tb-btn tb-btn--extra tb-btn--debug"
      @click="$emit('toggleHud')"
      title="Debug HUD"
    >
      <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <circle cx="12" cy="12" r="3" />
        <path d="M19.4 15a1.65 1.65 0 00.33 1.82l.06.06a2 2 0 01-2.83 2.83l-.06-.06a1.65 1.65 0 00-1.82-.33 1.65 1.65 0 00-1 1.51V21a2 2 0 01-4 0v-.09a1.65 1.65 0 00-1.08-1.51 1.65 1.65 0 00-1.82.33l-.06.06a2 2 0 01-2.83-2.83l.06-.06a1.65 1.65 0 00.33-1.82 1.65 1.65 0 00-1.51-1H3a2 2 0 010-4h.09a1.65 1.65 0 001.51-1.08 1.65 1.65 0 00-.33-1.82l-.06-.06a2 2 0 012.83-2.83l.06.06a1.65 1.65 0 001.82.33H9a1.65 1.65 0 001-1.51V3a2 2 0 014 0v.09a1.65 1.65 0 001 1.51 1.65 1.65 0 001.82-.33l.06-.06a2 2 0 012.83 2.83l-.06.06a1.65 1.65 0 00-.33 1.82V9c.26.6.77 1.02 1.51 1.08H21a2 2 0 010 4h-.09a1.65 1.65 0 00-1.51 1z" />
      </svg>
    </button>
  </div>
</template>

<script setup lang="ts">
defineProps<{
  sessionId: string
  stickyShift: boolean
  stickyCtrl: boolean
  keyboardUp: boolean
  stickyAlt: boolean
  activePanel: 'none' | 'numpad' | 'compose'
  keycastOn: boolean
}>()

defineEmits<{
  (e: 'sendKey', key: string): void
  (e: 'clipboard', op: 'paste'): void
  (e: 'toggleNumpad'): void
  (e: 'toggleCompose'): void
  (e: 'toggleShift'): void
  (e: 'toggleCtrl'): void
  (e: 'toggleAlt'): void
  (e: 'toggleHud'): void
  (e: 'toggleKeycast'): void
  (e: 'toggleKeyboard'): void
  (e: 'attach'): void
}>()
</script>

<style scoped>
.toolbar-strip {
  --tb-bg: #1c1c1e;
  --tb-btn-bg: #2c2c2e;
  --tb-btn-border: #3a3a3c;
  --tb-btn-depth: #111;
  --tb-btn-active: #3a3a3c;
  --tb-btn-color: #e8e8ea;

  display: flex;
  align-items: center;
  gap: 3px;
  padding: 4px 6px;
  background: var(--tb-bg);
  border-top: 1px solid var(--tb-btn-border);
  overflow-x: auto;
  overflow-y: hidden;
  -webkit-overflow-scrolling: touch;
  touch-action: pan-x;
  scrollbar-width: none;
}
.toolbar-strip::-webkit-scrollbar {
  display: none;
}

/* -- Base button ---------------------------------------------------- */
.tb-btn {
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 3px;
  min-width: 42px;
  height: 36px;
  padding: 0 10px;
  background: var(--tb-btn-bg);
  color: var(--tb-btn-color);
  border: 1px solid var(--tb-btn-border);
  border-bottom: 2px solid var(--tb-btn-depth);
  border-radius: 6px;
  font-size: 0.72rem;
  font-weight: 500;
  cursor: pointer;
  touch-action: manipulation;
  user-select: none;
  -webkit-user-select: none;
  white-space: nowrap;
  transition: background 0.08s, transform 0.08s;
}
.tb-btn:active {
  background: var(--tb-btn-active);
  transform: translateY(1px) scale(0.96);
  border-bottom-width: 1px;
}
.tb-btn span {
  font-size: 0.7rem;
}

/* -- Sticky modifier active (orange) ------------------------------- */
.tb-btn--sticky-active {
  background: #4a3000;
  border-color: #806000;
  border-bottom-color: #332000;
  color: #ffa000;
}
.tb-btn--sticky-active:active {
  background: #5a3800;
}

/* -- Numpad panel active ------------------------------------------- */
.tb-btn--panel-numpad {
  background: #1a2d44;
  border-color: #2a5a8a;
  border-bottom-color: #111;
  color: #8ec5ff;
}
.tb-btn--panel-numpad:active {
  background: #1e3550;
}

/* -- Compose panel active ------------------------------------------ */
.tb-btn--panel-compose {
  background: #0e2030;
  border-color: #1a4a7a;
  border-bottom-color: #111;
  color: #80b8ff;
}
.tb-btn--panel-compose:active {
  background: #162a40;
}

/* -- KeyCastr toggle: subdued when off, green when keystroke display is on ------ */
.tb-btn--keycast {
  color: #707078;
  border-color: #2a2a4a;
  border-bottom-color: #111;
  background: #1a1a2e;
}
.tb-btn--keycast:active { background: #222240; }
.tb-btn--keycast-on {
  color: #4ade80;
  border-color: #2a5a3a;
  background: #0e1e12;
}

/* -- Debug button (subdued) ---------------------------------------- */
.tb-btn--debug {
  color: #9090d0;
  border-color: #2a2a4a;
  border-bottom-color: #111;
  background: #1a1a2e;
}
.tb-btn--debug:active {
  background: #222240;
}

/* -- Divider ------------------------------------------------------ */
.tb-divider {
  flex-shrink: 0;
  width: 1px;
  height: 20px;
  background: #3a3a3c;
  margin: 0 2px;
  opacity: 0.5;
}

/* -- Extra buttons (page 2, scroll right) ------------------------- */
.tb-btn--extra {
  color: #a0c0e0;
  border-color: #2a3a5a;
  background: #0e1828;
  font-size: 0.66rem;
  min-width: 40px;
  padding: 0 8px;
}
.tb-btn--extra:active { background: #162840; }
.tb-btn--enter { color: #80c880; border-color: #2a4a2a; background: #0e1e0e; }
/* Backspace (delete-left) — neutral tone, keeps the red reserved for forward-delete. */
.tb-btn--bksp { color: #d0b0ff; border-color: #3a2a5a; background: #170e28; font-size: 0.82rem; }
.tb-btn--del { color: #ff8080; border-color: #4a2020; background: #1e0e0e; }

/* -- Active blue (keyboard toggle) --------------------------------- */
.tb-btn--active-blue {
  background: #0e2040;
  border-color: #2a5a9a;
  color: #60a0ff;
}

/* -- Collapse button (legacy, now keyboard toggle) ----------------- */
.tb-btn--collapse {
  min-width: 32px;
  padding: 0 5px;
  color: #808088;
  border-color: #333338;
  border-bottom-color: #111;
  background: #222226;
}
.tb-btn--collapse:active {
  background: #2a2a30;
}

.tb-btn--attach {
  color: #8ab4f8;
}

/* -- Paste button (clipboard) -------------------------------------- */
.tb-btn--paste {
  color: #c080ff;
}

/* -- Tab: icon-only, slightly narrower than text buttons but still a safe touch target -- */
.tb-btn--tab {
  min-width: 40px;
  padding: 0 8px;
}

/* -- Scroll fade hint on right edge ------------------------------- */
.toolbar-strip {
  position: relative;
}
.toolbar-strip::after {
  content: '';
  position: sticky;
  right: 0;
  flex-shrink: 0;
  width: 18px;
  height: 100%;
  background: linear-gradient(to right, transparent, var(--tb-bg));
  pointer-events: none;
}
</style>
