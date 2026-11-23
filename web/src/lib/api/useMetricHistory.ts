import { useQuery } from '@tanstack/react-query'
import { api } from './endpoints'
import { queryKeys } from './keys'

export interface RangePreset {
  id: string
  label: string
  ms: number
}

export const RANGES: readonly RangePreset[] = [
  { id: '15m', label: '15 minutes', ms: 15 * 60_000 },
  { id: '1h', label: '1 hour', ms: 60 * 60_000 },
  { id: '6h', label: '6 hours', ms: 6 * 3_600_000 },
  { id: '24h', label: '24 hours', ms: 24 * 3_600_000 },
  { id: '7d', label: '7 days', ms: 7 * 24 * 3_600_000 },
]

export const DEFAULT_RANGE_ID = '1h'
export const MAX_POINTS = 600

export function rangeById(id: string): RangePreset {
  return RANGES.find((r) => r.id === id) ?? RANGES[1]
}

/** Query parameters for a range ending at `now`. */
export function rangeParams(range: RangePreset, now: number) {
  return {
    scope: 'vm',
    from: new Date(now - range.ms).toISOString(),
    to: new Date(now).toISOString(),
    maxPoints: MAX_POINTS,
  }
}

/**
 * VM metric history for one range. `from` and `to` are computed inside the
 * queryFn so the key stays stable between renders. The key sits under
 * ['profiles', name], so the stream's resync invalidation refetches it.
 */
export function useMetricHistory(profile: string, rangeId: string) {
  const range = rangeById(rangeId)
  return useQuery({
    queryKey: queryKeys.metricsRange(profile, 'vm', range.id),
    queryFn: ({ signal }) => api.getMetrics(profile, rangeParams(range, Date.now()), signal),
    refetchOnWindowFocus: true,
  })
}
