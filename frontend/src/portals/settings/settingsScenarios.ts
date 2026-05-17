import type { ScenarioMachineConfig, LayoutPattern } from '@ce/composables/layout/types'

export const settingsScenarios: ScenarioMachineConfig = {
  initial: 'main',
  states: {
    'main': {
      layout: 'sidebar-main',
      slots: {
        navigator: { pane: 'category-rail', primitive: 'PanelPane', display: 'list', lifecycle: 'persistent' },
        primary: { pane: 'settings-form', primitive: 'FormPane', mode: 'page' },
      },
      on: {
        PROVIDER_AUTH: 'provider-auth',
      },
    },
    'provider-auth': {
      layout: 'sidebar-main',
      slots: {
        navigator: { pane: 'category-rail', primitive: 'PanelPane', display: 'list', lifecycle: 'persistent' },
        primary: { pane: 'settings-form', primitive: 'FormPane', mode: 'page' },
        secondary: { pane: 'browser-auth', primitive: 'CanvasPane', mode: 'interactive' },
      },
      on: {
        AUTH_CLOSED: 'main',
      },
    },
  },
}

// Mobile overrides: category becomes bottom sheet, everything solo
export const settingsBreakpointOverrides = {
  mobile: {
    'main': {
      layout: 'solo' as LayoutPattern,
      slots: {
        primary: { pane: 'settings-form', primitive: 'FormPane', mode: 'page' },
        navigator: { pane: 'category-rail', primitive: 'PanelPane', display: 'list', lifecycle: 'overlay-sheet' as const },
      },
    },
    'provider-auth': {
      layout: 'solo' as LayoutPattern,
      slots: {
        primary: { pane: 'browser-auth', primitive: 'CanvasPane', mode: 'interactive' },
        navigator: { pane: 'category-rail', primitive: 'PanelPane', display: 'list', lifecycle: 'overlay-sheet' as const },
      },
    },
  },
}
