<template>
  <div v-if="visible" class="auth-overlay" @click.self="$emit('dismiss')">
    <div class="auth-dialog">
      <h3>Terminal Authentication</h3>
      <p class="auth-hint">Enter the auth code displayed in the server terminal.</p>
      <input
        ref="inputRef"
        v-model="code"
        type="text"
        placeholder="6-character auth code"
        maxlength="10"
        autocomplete="off"
        @keyup.enter="submit"
      />
      <p v-if="error" class="auth-error">{{ error }}</p>
      <div class="auth-actions">
        <button @click="$emit('dismiss')">Cancel</button>
        <button class="btn-primary" @click="submit">Authenticate</button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch, nextTick } from 'vue'
import { useCliAuth } from '@/composables/cli/useCliAuth'
import { apiUrl } from '@ce/utils/runtimeBase'

const props = defineProps<{
  visible: boolean
}>()

const emit = defineEmits<{
  (e: 'dismiss'): void
  (e: 'authenticated'): void
}>()

const { setAuthCode } = useCliAuth()
const code = ref('')
const error = ref('')
const inputRef = ref<HTMLInputElement>()

// Auto-focus input when dialog opens.
watch(() => props.visible, async (v) => {
  if (v) {
    error.value = ''
    code.value = ''
    await nextTick()
    inputRef.value?.focus()
  }
})

async function submit() {
  const trimmed = code.value.trim()
  if (!trimmed) {
    error.value = 'Please enter an auth code.'
    return
  }

  // Validate by making a test request with the code.
  try {
    const headers: HeadersInit = { 'X-Auth-Code': trimmed }
    const resp = await fetch(apiUrl('/api/sessions'), { headers })
    if (resp.ok) {
      setAuthCode(trimmed)
      error.value = ''
      emit('authenticated')
    } else if (resp.status === 401) {
      error.value = 'Invalid auth code. Please try again.'
    } else {
      error.value = `Server error (${resp.status}). Please try again.`
    }
  } catch {
    error.value = 'Network error. Please check your connection.'
  }
}
</script>

<style scoped>
.auth-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0,0,0,0.6);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 2000;
}
.auth-dialog {
  background: #1e1e1e;
  color: #e0e0e0;
  padding: 24px;
  border-radius: 8px;
  min-width: 320px;
  max-width: 400px;
  border: 1px solid #444;
}
.auth-dialog h3 {
  margin-top: 0;
  color: #fff;
}
.auth-hint {
  color: #999;
  font-size: 0.875rem;
  margin-bottom: 16px;
}
.auth-dialog input {
  width: 100%;
  padding: 10px 12px;
  border: 1px solid #555;
  border-radius: 4px;
  margin-bottom: 8px;
  box-sizing: border-box;
  background: #2d2d2d;
  color: #e0e0e0;
  font-size: 1.1rem;
  font-family: 'Cascadia Code', monospace;
  letter-spacing: 0.15em;
  text-align: center;
}
.auth-dialog input:focus {
  border-color: #1976d2;
  outline: none;
}
.auth-error {
  color: #f44336;
  font-size: 0.8rem;
  margin: 4px 0 8px;
}
.auth-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 16px;
}
.auth-actions button {
  padding: 8px 16px;
  border: 1px solid #555;
  border-radius: 4px;
  cursor: pointer;
  background: #333;
  color: #e0e0e0;
}
.btn-primary {
  background: #1976d2 !important;
  color: white !important;
  border-color: #1976d2 !important;
}
</style>
