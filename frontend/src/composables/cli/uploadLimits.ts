/**
 * uploadLimits — the FRONTEND view of the SESSION file-upload cap.
 *
 * SSOT: the SERVER is the authority. It enforces the limit and, on a 413, returns `limit_mb`
 * so the real rejection message never hardcodes a number (see uploadFailure.ts). As of the
 * server-configurable cap (slice 4) the limit is a RUNTIME setting (GET/PUT /files/upload-limit),
 * so this module exposes a reactive `uploadMaxMb` pulled from the server rather than a fixed
 * constant. Client-side pre-checks read the ref to pre-empt an obviously-too-large upload before
 * spending the round-trip — a UX shortcut, not a second source of truth.
 *
 * SESSION_UPLOAD_MAX_MB stays exported as the DEFAULT/fallback (deepwork-pro's useWsAttachments
 * imports it, and it seeds the ref before the first fetch). Chat-image attachments
 * (useChatImageAttachments — its own smaller cap) are a DIFFERENT bounded context; do not unify.
 */
import { ref, computed } from 'vue'
import { getUploadLimit, setUploadLimit as apiSetUploadLimit, type UploadLimitInfo } from '@terminal/api/files'

/** Default/fallback cap (MB) — pro imports this; also seeds the reactive value pre-fetch. */
export const SESSION_UPLOAD_MAX_MB = 10
export const SESSION_UPLOAD_MAX_BYTES = SESSION_UPLOAD_MAX_MB * 1024 * 1024

// Module-scoped singletons: one reactive limit shared across every consumer, hydrated once from
// the server and updated in place when the user changes it via the tree's ⚙ quick-config.
const uploadMaxMb = ref<number>(SESSION_UPLOAD_MAX_MB)
const bounds = ref<{ defaultMb: number; ceilingMb: number; floorMb: number }>({
  defaultMb: SESSION_UPLOAD_MAX_MB,
  ceilingMb: 1024,
  floorMb: 1,
})
const uploadMaxBytes = computed(() => uploadMaxMb.value * 1024 * 1024)
let hydrated = false

export function useUploadLimit() {
  /** Fetch the server's current cap + bounds (once; pass force to re-pull). */
  async function load(force = false): Promise<void> {
    if (hydrated && !force) return
    const info = await getUploadLimit()
    if (info) {
      uploadMaxMb.value = info.maxMb
      bounds.value = { defaultMb: info.defaultMb, ceilingMb: info.ceilingMb, floorMb: info.floorMb }
      hydrated = true
    }
  }
  /** Persist a new cap; the server clamps to [floorMb, ceilingMb] and returns the effective value. */
  async function save(mb: number): Promise<number | null> {
    const eff = await apiSetUploadLimit(mb)
    if (eff != null) uploadMaxMb.value = eff
    return eff
  }
  return { uploadMaxMb, uploadMaxBytes, bounds, load, save }
}

export type { UploadLimitInfo }
