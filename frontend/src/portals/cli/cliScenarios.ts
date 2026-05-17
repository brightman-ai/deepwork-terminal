import type { ScenarioMachineConfig } from '@ce/composables/layout/types'
import type { LayoutPattern } from '@ce/composables/layout/types'

/**
 * CLI Portal 场景状态机。
 *
 * 状态语义:
 *   entry      — 初始加载中，尚无可用终端标签
 *   workbench  — 常规状态：有一个或多个终端标签处于活跃中
 *   settings   — CLI 配置面板展开（未来扩展点，当前预留）
 */
export const cliScenarios: ScenarioMachineConfig = {
  initial: 'entry',
  states: {
    // 加载阶段，尚未完成 reconcileSessions
    'entry': {
      layout: 'solo',
      slots: {
        primary: { pane: 'cli-terminal', primitive: 'WorkArea', mode: 'full' },
      },
      on: {
        TABS_READY: 'workbench',
      },
    },
    // 主工作台：有活跃终端标签
    'workbench': {
      layout: 'solo',
      slots: {
        primary: { pane: 'cli-terminal', primitive: 'WorkArea', mode: 'full' },
      },
      on: {
        OPEN_SETTINGS: 'settings',
        TABS_CLEARED:  'entry',
      },
    },
    // CLI 配置面板（预留状态，供后续扩展）
    'settings': {
      layout: 'solo',
      slots: {
        primary: { pane: 'cli-settings', primitive: 'PanelPane', mode: 'full' },
      },
      on: {
        CLOSE_SETTINGS: 'workbench',
        TABS_CLEARED:   'entry',
      },
    },
  },
}

/**
 * 移动端断点覆写：CLI Portal 在所有场景下均使用 solo 布局。
 * 移动端隐藏带宽/RTT 等次要信息，仅保留核心状态指示。
 */
export const cliBreakpointOverrides = {
  mobile: {
    'entry': {
      layout: 'solo' as LayoutPattern,
      slots: { primary: { pane: 'cli-terminal', primitive: 'WorkArea', mode: 'full' } },
    },
    'workbench': {
      layout: 'solo' as LayoutPattern,
      slots: { primary: { pane: 'cli-terminal', primitive: 'WorkArea', mode: 'full' } },
    },
    'settings': {
      layout: 'solo' as LayoutPattern,
      slots: { primary: { pane: 'cli-settings', primitive: 'PanelPane', mode: 'full' } },
    },
  },
}
