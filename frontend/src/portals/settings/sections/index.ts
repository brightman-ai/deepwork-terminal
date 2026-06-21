/**
 * Terminal-owned settings sections. Side-effect import (from the terminal router) registers them
 * into the 'settings' slot rendered by the @ce SettingsPortal. Also wires the host-injected authed
 * fetch so SHARED @ce sections (e.g. Internet Access) reach the terminal backend with cli auth.
 */
import { Info, Terminal, KeyRound, Bell } from 'lucide-vue-next'
import { definePortalSection, setSettingsApiFetch } from '@ce/framework/portal'
import { apiUrl } from '@ce/utils/runtimeBase'

// Replicates useCliAuth().cliFetch (X-CLI-Auth/X-Auth-Code from cli_auth_code) at module scope so
// it is set BEFORE any section renders — no composable, no provider-ancestor requirement.
setSettingsApiFetch((path, init) => {
  const headers = new Headers(init?.headers)
  const code = localStorage.getItem('cli_auth_code') || ''
  if (code) { headers.set('X-CLI-Auth', code); headers.set('X-Auth-Code', code) }
  return fetch(apiUrl(path), { ...init, headers })
})

definePortalSection({ slot: 'settings', id: 'terminal.system', group: 'terminal', label: 'System', icon: Info, order: 10, component: () => import('./SystemSection.vue') })
definePortalSection({ slot: 'settings', id: 'terminal.shell', group: 'terminal', label: 'Terminal', icon: Terminal, order: 20, component: () => import('./ShellSection.vue') })
definePortalSection({ slot: 'settings', id: 'terminal.notifications', group: 'terminal', label: 'Notifications', icon: Bell, order: 25, component: () => import('./NotificationsSection.vue') })
definePortalSection({ slot: 'settings', id: 'terminal.auth', group: 'terminal', label: 'Auth', icon: KeyRound, order: 30, component: () => import('./AuthSection.vue') })
