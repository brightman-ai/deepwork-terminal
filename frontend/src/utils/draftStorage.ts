/**
 * Generic KV draft storage backed by localStorage.
 * Key format: {namespace}#{entity_id}, e.g. "reply-draft#topic-123"
 * All keys are auto-prefixed with "deepwork_draft_".
 *
 * Supports optional TTL: if set, get() returns null after ttlMs has elapsed.
 */

const KEY_PREFIX = 'deepwork_draft_'

interface StoredEntry {
  value: string
  expiresAt?: number // Unix ms, undefined means no expiry
}

function fullKey(key: string): string {
  return KEY_PREFIX + key
}

export const draftStorage = {
  /**
   * Persist a draft value.
   * @param key   Namespaced key, e.g. "reply-draft#topic-123"
   * @param value String content to store
   * @param options.ttlMs Optional TTL in milliseconds
   */
  set(key: string, value: string, options?: { ttlMs?: number }): void {
    try {
      const entry: StoredEntry = {
        value,
        expiresAt: options?.ttlMs ? Date.now() + options.ttlMs : undefined,
      }
      localStorage.setItem(fullKey(key), JSON.stringify(entry))
    } catch {
      // localStorage may be unavailable (private browsing quota exceeded)
    }
  },

  /**
   * Retrieve a draft value, or null if missing / expired.
   */
  get(key: string): string | null {
    try {
      const raw = localStorage.getItem(fullKey(key))
      if (!raw) return null
      const entry: StoredEntry = JSON.parse(raw)
      if (entry.expiresAt !== undefined && Date.now() > entry.expiresAt) {
        localStorage.removeItem(fullKey(key))
        return null
      }
      return entry.value
    } catch {
      return null
    }
  },

  /**
   * Remove a draft entry.
   */
  delete(key: string): void {
    try {
      localStorage.removeItem(fullKey(key))
    } catch {
      // ignore
    }
  },
}
