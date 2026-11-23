import { describe, expect, it } from 'vitest'
import { diskSegments } from './diskUsage'

const zero = { imagesBytes: 0, containersBytes: 0, volumesBytes: 0, buildCacheBytes: 0, layersBytes: 0 }

describe('diskSegments', () => {
  it('gives proportional widths that add up to 100', () => {
    const { segments, total } = diskSegments({ ...zero, imagesBytes: 50, containersBytes: 25, volumesBytes: 20, buildCacheBytes: 5 })
    expect(total).toBe(100)
    expect(segments.map((s) => s.percent)).toEqual([50, 25, 20, 5])
    expect(segments.map((s) => s.label)).toEqual(['Images', 'Containers', 'Volumes', 'Build cache'])
  })

  it('handles all zeros without dividing by zero', () => {
    const { segments, total } = diskSegments(zero)
    expect(total).toBe(0)
    expect(segments.every((s) => s.percent === 0)).toBe(true)
  })

  it('ignores negative and non-finite values', () => {
    const { segments, total } = diskSegments({ ...zero, imagesBytes: 10, containersBytes: -5, volumesBytes: NaN, buildCacheBytes: Infinity })
    expect(total).toBe(10)
    expect(segments.map((s) => s.percent)).toEqual([100, 0, 0, 0])
  })

  it('does not count layers separately', () => {
    expect(diskSegments({ ...zero, imagesBytes: 4, layersBytes: 99 }).total).toBe(4)
  })
})
