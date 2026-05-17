import { Terminal } from 'lucide-vue-next'
import { definePortal } from '@ce/framework/portal'
import { portalRegistry } from '@ce/framework/portal'
import { cliScenarios } from './cliScenarios'
import { cliRoutes } from './routes'

export { default as CliPortal } from './CliPortal.vue'
export { cliScenarios, cliBreakpointOverrides } from './cliScenarios'
export { cliLayoutPolicy } from './cliLayoutPolicy'
export { cliRoutes } from './routes'
export { CliTabBar, CliAgentStatusStrip, CliTerminalView, CliCompanion } from './adapters'
// Re-export useAgentStatus so CLI portal consumers have a single import point.
export { useAgentStatus } from '@/composables/channel/useAgentStatus'

const cliPortalDescriptor = definePortal({
  id: 'cli',
  label: '终端',
  icon: Terminal,
  route: '/portal/cli',
  scenarios: cliScenarios,
  routes: cliRoutes,
})

portalRegistry.register(cliPortalDescriptor)

export default cliPortalDescriptor
