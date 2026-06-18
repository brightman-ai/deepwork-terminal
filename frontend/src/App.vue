<template>
  <router-view />
  <!-- Global AuthDialog — shown whenever a 401 is intercepted anywhere in the app -->
  <AuthDialog
    :visible="showAuthDialog"
    @dismiss="dismissAuthDialog"
    @authenticated="onAuthenticated"
  />
</template>

<script setup lang="ts">
// Root application component — renders the active route view.
import AuthDialog from '@terminal/components/terminal-session/AuthDialog.vue'
import { useCliAuth } from '@terminal/composables/cli/useCliAuth'
import { useBuildVersion } from '@terminal/composables/cli/useBuildVersion'

const { showAuthDialog, dismissAuthDialog } = useCliAuth()

// Auto-pick-up a new build when the tab regains focus — no manual refresh, session-safe.
useBuildVersion()

function onAuthenticated() {
  dismissAuthDialog()
  // Reload to retry all failed requests with the now-valid auth code.
  // Simple, reliable, covers all edge cases. Auth happens once per session.
  window.location.reload()
}
</script>
