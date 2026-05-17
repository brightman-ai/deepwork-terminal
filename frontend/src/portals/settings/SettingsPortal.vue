<script setup lang="ts">
import { usePortalRuntime } from '@ce/composables/layout/usePortalRuntime'
import { SlotGridRenderer } from '@ce/components/pane'
import { settingsScenarios, settingsBreakpointOverrides } from './settingsScenarios'
import { settingsLayoutPolicy } from './settingsLayoutPolicy'
import { useSettingsState } from './useSettingsState'
import { SettingsCategoryRail, SettingsFormView, SettingsProviderAuth } from './adapters'
import { Globe } from 'lucide-vue-next'

const runtime = usePortalRuntime({
  portalId: 'settings',
  scenarios: settingsScenarios,
  breakpointOverrides: settingsBreakpointOverrides,
  layoutPolicy: settingsLayoutPolicy,
})

const {
  breakpoint, dontDisturb, manualOpen,
  slotGrid, onGridResize, onDrop, onTabActivate, onTabClose, onCollapse, onResizeReset,
  activeCategory, providerAccountId,
  hasNavigator, hasSecondary, navigatorLifecycle,
  secondaryVisible, secondaryBadge,
  mobileActiveTab, mobileTabs, switchMobileTab,
} = useSettingsState(runtime)
</script>

<template>
  <div class="settings-portal" data-testid="settings-portal">
    <div class="settings-portal__body" :class="[breakpoint.isMobile.value && 'layout-mobile']">
      <template v-if="!breakpoint.isMobile.value">
        <SlotGridRenderer
          :grid="slotGrid.grid.value"
          :collapsed-slots="slotGrid.state.value.collapsedSlots"
          @resize="onGridResize"
          @resize-reset="onResizeReset"
          @drop="onDrop"
          @tab-activate="onTabActivate"
          @tab-close="onTabClose"
          @collapse="onCollapse"
        >
          <template #navigator>
            <aside v-if="hasNavigator && navigatorLifecycle === 'persistent'" class="settings-portal__navigator h-full">
              <SettingsCategoryRail :active-category="activeCategory" />
            </aside>
          </template>
          <template #primary>
            <main class="settings-portal__primary h-full flex flex-col bg-background">
              <SettingsFormView :active-category="activeCategory" />
            </main>
          </template>
          <template #secondary="{ collapsed }">
            <aside v-if="hasSecondary && secondaryVisible && !collapsed" class="settings-portal__secondary h-full">
              <div class="h-full flex flex-col border-l border-border">
                <SettingsProviderAuth :provider-account-id="providerAccountId" />
              </div>
            </aside>
          </template>
        </SlotGridRenderer>

        <div v-if="hasSecondary && !secondaryVisible && secondaryBadge > 0" class="settings-portal__secondary-badge">
          <button
            class="flex items-center gap-1.5 px-2 py-1 text-xs rounded-l-md border border-r-0 border-border bg-background hover:bg-muted transition-colors shadow-sm"
            @click="manualOpen('secondary'); dontDisturb.open('secondary')"
          >
            <Globe class="size-3.5" />
            <span class="font-medium tabular-nums">{{ secondaryBadge }}</span>
          </button>
        </div>
      </template>

      <template v-else>
        <div v-if="mobileActiveTab === 'categories'" class="h-full flex flex-col">
          <SettingsCategoryRail :active-category="activeCategory" />
        </div>
        <div v-else-if="mobileActiveTab === 'settings'" class="h-full flex flex-col bg-background">
          <SettingsFormView :active-category="activeCategory" />
        </div>
        <div v-else-if="mobileActiveTab === 'browser' && hasSecondary" class="settings-portal__mobile-browser">
          <SettingsProviderAuth :provider-account-id="providerAccountId" />
        </div>
      </template>
    </div>

    <nav v-if="breakpoint.isMobile.value" class="settings-portal__mobile-tabs">
      <button
        v-for="tab in mobileTabs"
        :key="tab.id"
        class="mobile-tab"
        :class="{ 'mobile-tab--active': mobileActiveTab === tab.id }"
        @click="switchMobileTab(tab.id)"
      >
        <component :is="tab.icon" class="size-4.5" />
        <span>{{ tab.label }}</span>
        <span v-if="tab.badge" class="mobile-tab__badge">{{ tab.badge }}</span>
      </button>
    </nav>
  </div>
</template>

<style scoped>
.settings-portal {
  display: flex;
  flex-direction: column;
  height: 100%;
  overflow: hidden;
  position: relative;
}
.settings-portal__body {
  flex: 1;
  overflow: hidden;
  min-height: 0;
  position: relative;
}
.layout-mobile {
  display: grid;
  grid-template-columns: 1fr;
  grid-template-rows: 1fr;
  position: relative;
}
.settings-portal__navigator,
.settings-portal__primary,
.settings-portal__secondary {
  overflow: hidden;
  min-height: 0;
  min-width: 0;
}
.settings-portal__secondary-badge {
  position: absolute;
  right: 0;
  top: 50%;
  transform: translateY(-50%);
  z-index: 10;
}
.settings-portal__mobile-browser {
  position: absolute;
  inset: 0;
  background: var(--background);
  z-index: 20;
}
.settings-portal__mobile-tabs {
  display: flex;
  align-items: stretch;
  border-top: 1px solid hsl(var(--border));
  background: hsl(var(--background));
  shrink: 0;
  padding-bottom: env(safe-area-inset-bottom, 0px);
}
.mobile-tab {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 2px;
  padding: 8px 4px;
  font-size: 10px;
  color: hsl(var(--muted-foreground));
  position: relative;
  transition: color 0.15s;
}
.mobile-tab--active { color: hsl(var(--primary)); }
.mobile-tab__badge {
  position: absolute;
  top: 4px;
  right: calc(50% - 16px);
  min-width: 16px;
  height: 16px;
  background: hsl(var(--destructive));
  color: hsl(var(--destructive-foreground));
  border-radius: 8px;
  font-size: 9px;
  font-weight: 600;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 0 4px;
  line-height: 1;
}
</style>
