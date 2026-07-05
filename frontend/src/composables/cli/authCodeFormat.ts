/**
 * formatAuthCode — game-activation-code style formatting for the terminal auth code as you type.
 * The default code is `XXXX-XXXX` (e.g. E3X1-M6T2); auth matching is case-insensitive (v0.7.0),
 * so we uppercase + strip non-alphanumerics + auto-insert the single dash after the 4th char.
 * NOT truncated past 8 — a longer custom code keeps its tail (dash still after char 4) rather
 * than being silently cut. Empty in → empty out.
 */
export function formatAuthCode(raw: string): string {
  const s = (raw || '').toUpperCase().replace(/[^A-Z0-9]/g, '')
  return s.length > 4 ? `${s.slice(0, 4)}-${s.slice(4)}` : s
}
