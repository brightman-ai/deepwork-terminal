import { ref, computed, watch, onUnmounted } from 'vue'
import { Settings, Globe } from 'lucide-vue-next'
import { useSlotGrid, useSlotGridHandlers, resolveLayout, saveSlotGridLayout, fetchServerLayout } from '@ce/composables/layout/slotGrid'
import type { PortalRuntimeResult } from '@ce/composables/layout/usePortalRuntime'

const PORTAL_ID = 'settings'

export type MobileTabId = 'settings' | 'browser'

interface MobileTab {
  id: MobileTabId
  label: string
  icon: typeof Settings
  badge?: number
}

export function useSettingsState(runtime: PortalRuntimeResult) {
  const { layout, scenario, breakpoint, policy, dontDisturb, bus } = runtime
  const { pattern, slots, hasSlot, getSlot } = layout
  const { slotExpanded, slotBadges, processEvent, manualOpen } = policy

  // ─── Platform detection ───────────────────────────────────────────────────────
  const platform = ((): 'wails-macos' | 'wails-windows' | 'browser' => {
    const isWails = !!(window as unknown as Record<string, unknown>).__wails ||
      !!(window as unknown as Record<string, unknown>).wails
    if (isWails) {
      return navigator.userAgent.includes('Windows') ? 'wails-windows' : 'wails-macos'
    }
    return 'browser'
  })()

  // ─── SlotGrid ────────────────────────────────────────────────────────────────
  const slotGrid = useSlotGrid(resolveLayout(PORTAL_ID, scenario.current.value, pattern.value, platform, slots.value))

  watch(
    () => scenario.current.value,
    async (newScenario) => {
      const serverGrid = await fetchServerLayout(PORTAL_ID, newScenario, platform)
      if (serverGrid) {
        slotGrid.restore(JSON.stringify(serverGrid))
      } else {
        slotGrid.restore(JSON.stringify(resolveLayout(PORTAL_ID, newScenario, pattern.value, platform, slots.value)))
      }
    },
  )

  function onGridResize(parentPath: number[], index: number, deltaPx: number): void {
    const bodyEl = document.querySelector('.settings-portal__body') as HTMLElement | null
    const containerWidth = bodyEl?.offsetWidth ?? window.innerWidth
    const containerHeight = bodyEl?.offsetHeight ?? window.innerHeight
    slotGrid.resizeSlot(parentPath, index, deltaPx, containerWidth, containerHeight)
    saveSlotGridLayout(PORTAL_ID, scenario.current.value, platform, slotGrid.grid.value)
  }

  const { onDrop, onTabActivate, onTabClose, onCollapse, onResizeReset } = useSlotGridHandlers({
    portalId: PORTAL_ID,
    scenario: scenario.current,
    platform,
    slotGrid,
    dontDisturb,
  })

  // ─── Category / provider auth state ──────────────────────────────────────────
  const activeCategory = ref<string>('system-info')
  const providerAccountId = ref<string>('')

  bus.on('settings.category', (payload) => {
    const p = payload as { categoryId?: string } | undefined
    if (p?.categoryId) {
      activeCategory.value = p.categoryId
      // Mobile: the category nav is a sticky segmented control inside the settings
      // panel, so the content panel is already visible — no tab switch needed.
      // One tap on a chip = that category's settings render in place.
    }
  })

  bus.on('provider.auth_needed', (payload) => {
    const p = payload as { accountId?: string } | undefined
    if (p?.accountId) providerAccountId.value = p.accountId
    scenario.send('PROVIDER_AUTH')
    manualOpen('secondary')
    processEvent('provider.auth_needed', payload)
  })

  bus.on('provider.auth_closed', () => {
    scenario.send('AUTH_CLOSED')
    providerAccountId.value = ''
  })

  onUnmounted(() => {
    // Future cleanup
  })

  // ─── Derived slot visibility ──────────────────────────────────────────────────
  const navigatorSlot = computed(() => getSlot('navigator'))
  const hasNavigator = computed(() => hasSlot('navigator'))
  const hasSecondary = computed(() => hasSlot('secondary'))
  const navigatorLifecycle = computed(() => navigatorSlot.value?.lifecycle ?? 'persistent')
  const secondaryVisible = computed(() =>
    hasSecondary.value &&
    !dontDisturb.isClosed('secondary') &&
    (slotExpanded.value['secondary'] !== false),
  )
  const secondaryBadge = computed(() => slotBadges.value['secondary'] ?? 0)

  // ─── Mobile tabs ──────────────────────────────────────────────────────────────
  const mobileActiveTab = ref<MobileTabId>('settings')

  const mobileTabs = computed<MobileTab[]>(() => {
    const tabs: MobileTab[] = [
      { id: 'settings', label: '设置', icon: Settings },
    ]
    if (hasSecondary.value) {
      tabs.push({ id: 'browser', label: '登录', icon: Globe, badge: secondaryBadge.value || undefined })
    }
    return tabs
  })

  function switchMobileTab(tabId: MobileTabId) {
    mobileActiveTab.value = tabId
    if (tabId === 'browser') slotBadges.value['secondary'] = 0
  }

  return {
    scenario, breakpoint, dontDisturb, slotBadges, manualOpen,
    slotGrid, onGridResize, onDrop, onTabActivate, onTabClose, onCollapse, onResizeReset,
    activeCategory, providerAccountId,
    hasNavigator, hasSecondary, navigatorLifecycle,
    secondaryVisible, secondaryBadge,
    mobileActiveTab, mobileTabs, switchMobileTab,
  }
}
