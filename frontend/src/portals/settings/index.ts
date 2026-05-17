import { Settings } from 'lucide-vue-next'
import { definePortal } from '@ce/framework/portal'
import { portalRegistry } from '@ce/framework/portal'
import { settingsScenarios } from './settingsScenarios'
import { settingsRoutes } from './routes'

export { default as SettingsPortal } from './SettingsPortal.vue'
export { settingsScenarios, settingsBreakpointOverrides } from './settingsScenarios'
export { settingsLayoutPolicy } from './settingsLayoutPolicy'
export { settingsRoutes } from './routes'
export {
  SettingsCategoryRail,
  SettingsFormView,
  SettingsProviderAuth,
  SettingsWizardView,
} from './adapters'

const settingsPortalDescriptor = definePortal({
  id: 'settings',
  label: '设置',
  icon: Settings,
  route: '/portal/settings',
  scenarios: settingsScenarios,
  routes: settingsRoutes,
})

portalRegistry.register(settingsPortalDescriptor)

export default settingsPortalDescriptor
