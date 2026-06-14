<template>
  <div class="terminal-list" data-testid="terminal-list-page">
    <div class="cli-header">
      <h2>Terminals</h2>
      <button class="btn-create" data-testid="new-session-btn" @click="openCreateDialog">
        + New Session
      </button>
    </div>

    <div v-if="loading" class="cli-loading">Loading sessions...</div>

    <div v-else-if="sessions.length === 0" class="cli-empty">
      <p>No active sessions. Create one to get started.</p>
    </div>

    <div v-else class="cli-cards">
      <div
        v-for="session in sessions"
        :key="session.id"
        class="session-card"
        @click="openSession(session.id)"
      >
        <div class="session-status">
          <span
            class="status-dot"
            :class="statusClass(session.status)"
          />
          <span class="session-name">{{ session.title || session.name }}</span>
        </div>
        <div class="session-meta">
          <span v-if="(session as any).agentTool" class="session-agent-badge" :class="'agent-' + (session as any).agentStatus">
            <span class="status-dot-mini" :class="'dot-' + (session as any).agentStatus" />
            {{ (session as any).agentTool }}
          </span>
          <span class="session-status-text">{{ session.engine || 'shell' }} · {{ session.status }}</span>
          <span v-if="session.cwd" class="session-cwd">{{ session.cwd }}</span>
          <span class="session-time">{{ formatTime(session.lastActive || session.last_seen || '') }}</span>
        </div>
        <button
          class="btn-destroy"
          @click.stop="destroySession(session.id)"
          title="Destroy session"
        >
          &times;
        </button>
      </div>
    </div>

    <!-- Create Dialog -->
    <div v-if="showCreateDialog" class="dialog-overlay" data-testid="cli-create-dialog" @click.self="closeCreateDialog">
      <div class="dialog">
        <h3>Create New Session</h3>
        <input
          v-model="newSessionName"
          type="text"
          placeholder="Session name (auto: MMdd-HHmm)"
          data-testid="terminal-name-input"
          @keyup.enter="createSession"
        />
        <input
          v-model="newSessionCwd"
          type="text"
          placeholder="Working directory (optional)"
          data-testid="terminal-cwd-input"
          @keyup.enter="createSession"
        />
        <p v-if="createError" class="dialog-error">{{ createError }}</p>
        <div class="dialog-actions">
          <button :disabled="creating" @click="closeCreateDialog">Cancel</button>
          <button class="btn-primary" data-testid="terminal-create-submit" :disabled="creating" @click="createSession">
            {{ creating ? 'Creating...' : 'Create' }}
          </button>
        </div>
      </div>
    </div>

    <!-- Auth Dialog (BUG-2) -->
    <AuthDialog
      :visible="showAuthDialog"
      @dismiss="dismissAuthDialog"
      @authenticated="onAuthenticated"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import type { TerminalSessionInfo } from '@terminal/types/terminal'
import { useCliAuth } from '@terminal/composables/cli/useCliAuth'
import AuthDialog from '@terminal/components/terminal-session/AuthDialog.vue'

const router = useRouter()
const { cliFetch, showAuthDialog, dismissAuthDialog } = useCliAuth()
const sessions = ref<TerminalSessionInfo[]>([])
const loading = ref(true)
const showCreateDialog = ref(false)
const newSessionName = ref('')
const newSessionCwd = ref('')
const createError = ref('')
const creating = ref(false)

async function fetchSessions() {
  try {
    const resp = await cliFetch('/api/sessions')
    if (resp.ok) {
      sessions.value = await resp.json()
      // Agent state is now bundled in the list API response (agentTool, agentStatus).
      // No separate enrichment requests — zero connection pool pressure.
    }
  } catch (err) {
    console.error('Failed to fetch sessions:', err)
  } finally {
    loading.value = false
  }
}

async function createSession() {
  if (creating.value) return
  creating.value = true
  createError.value = ''
  try {
    const resp = await cliFetch('/api/sessions', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        name: newSessionName.value || undefined,
        title: newSessionName.value || undefined,
        engine: 'shell',
        cwd: newSessionCwd.value || undefined,
      }),
    })
    if (resp.ok) {
      const data = await resp.json()
      showCreateDialog.value = false
      newSessionName.value = ''
      newSessionCwd.value = ''
      router.push(`/cli/${data.session_id || data.id}`)
    } else {
      createError.value = await responseError(resp)
    }
  } catch (err) {
    console.error('Failed to create session:', err)
    createError.value = err instanceof Error ? err.message : 'Failed to create session'
  } finally {
    creating.value = false
  }
}

async function responseError(resp: Response): Promise<string> {
  const data = await resp.json().catch(() => ({})) as { error?: string; detail?: string; message?: string }
  return data.detail || data.error || data.message || `HTTP ${resp.status}`
}

function closeCreateDialog() {
  showCreateDialog.value = false
  createError.value = ''
}

function openCreateDialog() {
  createError.value = ''
  showCreateDialog.value = true
}

async function destroySession(id: string) {
  try {
    await cliFetch(`/api/sessions/${id}`, { method: 'DELETE' })
    sessions.value = sessions.value.filter(s => s.id !== id)
  } catch (err) {
    console.error('Failed to destroy session:', err)
  }
}

function onAuthenticated() {
  dismissAuthDialog()
  fetchSessions()
}

function openSession(id: string) {
  router.push(`/cli/${id}`)
}

function statusClass(status: string): string {
  switch (status) {
    case 'running': return 'status-running'
    case 'exited': return 'status-exited'
    default: return 'status-idle'
  }
}

function formatTime(iso: string): string {
  if (!iso) return ''
  const d = new Date(iso)
  return d.toLocaleTimeString()
}

onMounted(fetchSessions)
</script>

<style scoped>
.terminal-list {
  max-width: 800px;
  margin: 0 auto;
  padding: 24px 16px;
  color: hsl(var(--foreground));
}
.cli-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 24px;
}
.cli-header h2 {
  margin: 0;
  font-size: 1.5rem;
  color: hsl(var(--foreground));
}
.btn-create {
  padding: 8px 16px;
  background: #1976d2;
  color: white;
  border: none;
  border-radius: 4px;
  cursor: pointer;
}
.btn-create:hover { background: #1565c0; }
.cli-loading, .cli-empty {
  text-align: center;
  padding: 48px;
  color: hsl(var(--muted-foreground));
}
.cli-cards {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.session-card {
  display: flex;
  align-items: center;
  padding: 16px;
  background: hsl(var(--card));
  border-radius: 8px;
  cursor: pointer;
  transition: background 0.15s;
  position: relative;
}
.session-card:hover { background: hsl(var(--accent)); }
.session-status {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: 1;
}
.status-dot {
  width: 10px;
  height: 10px;
  border-radius: 50%;
  display: inline-block;
}
.status-running { background: #4caf50; }
.status-exited { background: #9e9e9e; }
.status-idle { background: #ffc107; }
.session-name {
  font-weight: 500;
  color: hsl(var(--foreground));
}
.session-meta {
  display: flex;
  gap: 16px;
  color: hsl(var(--muted-foreground));
  font-size: 0.875rem;
}
.session-status-text {
  color: hsl(var(--muted-foreground));
}
.session-agent-badge {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 1px 8px;
  border-radius: 10px;
  font-size: 0.75rem;
  font-weight: 500;
  background: rgba(0, 0, 0, 0.08);
  color: hsl(var(--muted-foreground));
}
.agent-running { background: rgba(76, 141, 255, 0.12); color: #4C8DFF; }
.agent-waiting { background: rgba(255, 193, 7, 0.12); color: #FFC107; }
.agent-done { background: rgba(76, 175, 80, 0.12); color: #4CAF50; }
.status-dot-mini {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  display: inline-block;
  flex-shrink: 0;
}
.dot-running { background: #4C8DFF; }
.dot-idle { background: #FFC107; }
.dot-waiting { background: #FFC107; animation: pulse 1.5s infinite; }
.dot-done { background: #4CAF50; }
@keyframes pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.4; }
}
.btn-destroy {
  position: absolute;
  right: 12px;
  top: 50%;
  transform: translateY(-50%);
  background: none;
  border: none;
  font-size: 1.25rem;
  color: hsl(var(--muted-foreground));
  cursor: pointer;
  padding: 4px 8px;
}
.btn-destroy:hover { color: #f44336; }
.dialog-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0,0,0,0.5);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 1000;
}
.dialog {
  background: hsl(var(--popover));
  color: hsl(var(--foreground));
  padding: 24px;
  border-radius: 8px;
  min-width: 320px;
}
.dialog h3 {
  margin-top: 0;
  color: hsl(var(--foreground));
}
.dialog input {
  width: 100%;
  padding: 8px 12px;
  border: 1px solid hsl(var(--border));
  border-radius: 4px;
  margin-bottom: 16px;
  box-sizing: border-box;
  background: hsl(var(--input));
  color: hsl(var(--foreground));
}
.dialog-error {
  margin: -6px 0 14px;
  color: #f87171;
  font-size: 0.875rem;
}
.dialog-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}
.dialog-actions button {
  padding: 8px 16px;
  border: 1px solid hsl(var(--border));
  border-radius: 4px;
  cursor: pointer;
  background: hsl(var(--popover));
  color: hsl(var(--foreground));
}
.dialog-actions button:disabled {
  opacity: 0.55;
  cursor: not-allowed;
}
.btn-primary {
  background: #1976d2 !important;
  color: white !important;
  border-color: #1976d2 !important;
}

</style>
