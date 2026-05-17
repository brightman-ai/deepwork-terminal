/**
 * useDeviceDetection — detects mobile devices via UA + touch capability.
 * [Ref: CAP-mobile-interaction S2]
 */
import { ref, onMounted } from 'vue'
import { detectCliRuntimeProfile } from '@/composables/cli/useCliRuntimeMode'

export function useDeviceDetection() {
  const isMobile = ref(false)

  function detect(): boolean {
    return detectCliRuntimeProfile().usesMobileOverlay
  }

  onMounted(() => {
    isMobile.value = detect()
  })

  return { isMobile }
}
