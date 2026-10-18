export type SearchIndexState = 'building' | 'ready' | 'error' | ''

export function searchPollDelay(indexState: SearchIndexState, hasScanError: boolean): number {
  // The index builder publishes generations faster than the UI can usefully repaint. Polling
  // twice per second repeatedly re-runs the same prefix match and competes with directory reads;
  // a 1.5s cadence keeps the result fresh while the builder is active without turning a slow scan
  // into a request/JSON-parse loop.
  if (indexState === 'building') return 1500
  if (indexState === 'error' || hasScanError) return 5 * 60 * 1000
  return 30 * 1000
}
