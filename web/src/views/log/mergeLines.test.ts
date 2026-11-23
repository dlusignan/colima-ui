import { describe, expect, it } from 'vitest'
import { mergeLines } from './mergeLines'

const l = (seq: number, line = `l${seq}`) => ({ seq, line })

describe('mergeLines', () => {
  it('orders by seq and drops duplicates, keeping the first copy', () => {
    expect(mergeLines([l(1), l(2), l(3, 'rest')], [l(3, 'live'), l(4), l(5)])).toEqual([
      l(1),
      l(2),
      l(3, 'rest'),
      l(4),
      l(5),
    ])
  })

  it('fills a gap left by a dropped connection', () => {
    expect(mergeLines([l(1), l(2), l(3), l(4)], [l(2), l(4)])).toHaveLength(4)
  })

  it('handles empty input and a truncated head', () => {
    expect(mergeLines([], [])).toEqual([])
    expect(mergeLines([l(101), l(102)], [l(103)]).map((x) => x.seq)).toEqual([101, 102, 103])
  })
})
