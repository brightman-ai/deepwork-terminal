/**
 * useDeviceDetection — detects mobile devices via UA + touch capability.
 * [Ref: CAP-mobile-interaction S2]
 */
import { ref, onMounted } from 'vue'
import { detectCliRuntimeProfile } from '@terminal/composables/cli/useCliRuntimeMode'

export function useDeviceDetection() {
  function detect(): boolean {
    return detectCliRuntimeProfile().usesMobileOverlay
  }

  // Detect the viewport SYNCHRONOUSLY at setup so the very first paint already renders the correct
  // mobile-vs-desktop layout. Seeding this to `false` used to make F5 flash the DESKTOP tmux bar
  // (pane-level "1.1 2.1 …") for one frame before onMounted flipped it to mobile (window-level
  // "1 2 3 …"). detectCliRuntimeProfile() is pure + SSR-guarded, so calling it here is safe.
  const isMobile = ref(detect())

  // Belt-and-suspenders re-detect on mount (covers any UA/touch signal not ready at setup time).
  onMounted(() => {
    isMobile.value = detect()
  })

  return { isMobile }
}
