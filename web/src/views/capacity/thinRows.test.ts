import { describe, expect, it } from 'vitest'
import { thinRows } from './thinRows'

describe('thinRows', () => {
  it('keeps short lists whole', () => {
    expect(thinRows([1, 2, 3], 5)).toEqual([1, 2, 3])
  })
  it('thins to the limit and keeps both ends', () => {
    const items = Array.from({ length: 100 }, (_, i) => i)
    const out = thinRows(items, 10)
    expect(out).toHaveLength(10)
    expect(out[0]).toBe(0)
    expect(out[9]).toBe(99)
  })
})
