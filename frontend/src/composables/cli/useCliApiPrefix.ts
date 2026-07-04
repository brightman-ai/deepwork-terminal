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

/**
 * Path for a REMOTE peer's terminal API. A peer is always a standalone
 * deepwork-terminal, which serves its API at /api/* (prefix ''), REGARDLESS of how
 * THIS instance is mounted. So peer-directed calls (probe, remote session create,
 * remote WS) must use this fixed mount — NOT cliApi, which carries the LOCAL embed
 * prefix (e.g. pro's '/cli'). For a standalone caller (prefix '') this equals
 * cliApi, so the change is a no-op there; for an embedding host it is the fix that
 * makes pro→terminal-peer resolve to the peer's real /api/* path. [remote-mesh RT-*]
 */
export function peerApi(path: string): string { return `/api${path}` }
