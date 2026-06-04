/**
 * Injectable API mount prefix. Standalone deepwork-terminal serves the terminal API at
 * /api/*; an embedding host (e.g. deepwork-pro) mounts it under /api/cli/* and calls
 * setCliApiPrefix('/cli') once at bootstrap. cliApi('/sessions/x') → '/api/sessions/x'
 * standalone, '/api/cli/sessions/x' when embedded.
 */
let _prefix = ''
export function setCliApiPrefix(p: string): void { _prefix = p }
export function getCliApiPrefix(): string { return _prefix }
export function cliApi(path: string): string { return `/api${_prefix}${path}` }
