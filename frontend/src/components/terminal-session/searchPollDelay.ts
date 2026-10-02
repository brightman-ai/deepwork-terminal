export type SearchIndexState = 'building' | 'ready' | 'error' | ''

export function searchPollDelay(indexState: SearchIndexState, hasScanError: boolean): number {
  if (indexState === 'building') return 500
  if (indexState === 'error' || hasScanError) return 5 * 60 * 1000
  return 30 * 1000
}
