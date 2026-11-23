import { describe, expect, it } from 'vitest'
import { queryKeys } from './keys'
import { MAX_POINTS, RANGES, rangeById, rangeParams } from './useMetricHistory'

describe('rangeParams', () => {
  it('computes from and to from the clock, one value each', () => {
    const now = Date.UTC(2026, 0, 1, 12)
    const p = rangeParams(rangeById('6h'), now)
    expect(p).toEqual({
      scope: 'vm',
      from: '2026-01-01T06:00:00.000Z',
      to: '2026-01-01T12:00:00.000Z',
      maxPoints: MAX_POINTS,
    })
  })
  it('offers 15m, 1h, 6h, 24h and 7d and falls back to 1h', () => {
    expect(RANGES.map((r) => r.id)).toEqual(['15m', '1h', '6h', '24h', '7d'])
    expect(rangeById('bogus').id).toBe('1h')
  })
  it('keys history under the profile metrics prefix', () => {
    expect(queryKeys.metricsRange('a', 'vm', '1h').slice(0, 3)).toEqual(['profiles', 'a', 'metrics'])
  })
})
