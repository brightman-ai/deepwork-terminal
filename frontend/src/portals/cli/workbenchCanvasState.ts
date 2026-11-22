/**
 * workbenchCanvasState — Pure visibility resolver for Task Workbench v11 main canvas.
 *
 * Core invariant (REQ-TW-01): `<CliTerminalView>` is ALWAYS mounted and toggled strictly
 * via `v-show="canvas.showTerminalView"` (NEVER `v-if`), so live xterm instances and
 * WebSockets survive transitions to BotWorkspaceView, CenterFilePreview (italic Preview Tab),
 * SessionTranscriptView (`💬 Chat`), and DetachedTerminalCard.
 */
import type { SpaceKind, TabViewMode } from '@terminal/composables/cli/useTaskWorkbench'

export interface WorkbenchCanvasInput {
  activeSpaceKind: SpaceKind
  hasDetachedCard: boolean
  hasCenterPreview: boolean
  centerPreviewActive: boolean
  hasSplitPreview: boolean
  isMobile: boolean
  activeTabViewMode: TabViewMode
  hasActiveSession: boolean
}

export interface WorkbenchCanvasVisibility {
  showTerminalView: boolean
  showDetachedCard: boolean
  showTranscriptView: boolean
  showCenterPreview: boolean
  showBotWorkspace: boolean
  showSplitPreview: boolean
}

export function resolveWorkbenchCanvas(
  input: WorkbenchCanvasInput,
): WorkbenchCanvasVisibility {
  const isBot = input.activeSpaceKind === 'bot'
  const showBotWorkspace = isBot
  const showCenterPreview = !isBot && input.hasCenterPreview && input.centerPreviewActive
  const showDetachedCard = !isBot && !showCenterPreview && input.hasDetachedCard
  const showTranscriptView =
    !isBot &&
    !showCenterPreview &&
    !showDetachedCard &&
    input.activeTabViewMode === 'transcript' &&
    input.hasActiveSession
  const showTerminalView =
    !isBot &&
    !showCenterPreview &&
    !showDetachedCard &&
    !showTranscriptView
  const showSplitPreview =
    !isBot &&
    !input.isMobile &&
    !showCenterPreview &&
    input.hasSplitPreview

  return {
    showTerminalView,
    showDetachedCard,
    showTranscriptView,
    showCenterPreview,
    showBotWorkspace,
    showSplitPreview,
  }
}

/**
 * R2-01: When the Teleported docked ('split') ResourceDrawer is open while a non-terminal
 * companion view (CenterFilePreview / SessionTranscriptView / DetachedTerminalCard) is shown,
 * compute the exact padding inset so toolbar actions and design-pick-popover never sit behind
 * the fixed right/left drawer. Returns `{}` when CliTerminalView is visible (it squeezes itself)
 * or in BotWorkspaceView.
 */
export function resolveCompanionDrawerInset(
  canvas: WorkbenchCanvasVisibility,
  squeezePx: number,
  dock: 'left' | 'right' = 'right',
): Record<string, string> {
  if (canvas.showTerminalView || canvas.showBotWorkspace) return {}
  if (!Number.isFinite(squeezePx) || squeezePx <= 0) return {}
  return dock === 'left'
    ? { paddingLeft: `${Math.round(squeezePx)}px` }
    : { paddingRight: `${Math.round(squeezePx)}px` }
}
