import { describe, expect, it, vi } from 'vitest'
import { SeriesBuffer, type RawPoint } from './SeriesBuffer'

const at = (sec: number, over: Partial<RawPoint> = {}): RawPoint => ({
  takenAt: new Date(Date.UTC(2026, 0, 1, 0, 0, sec)).toISOString(),
  cpuPercent: 10,
  memoryBytes: 100,
  diskBytes: 1000,
  ...over,
})

describe('SeriesBuffer', () => {
  it('loads history in time order and keeps null as null', () => {
    const b = new SeriesBuffer(3_600_000, 0)
    b.setHistory([at(10), at(0, { cpuPercent: null }), at(5)])
    expect(b.column('cpu').values).toEqual([null, 10, 10])
    expect(b.column('cpu').t[0]).toBeLessThan(b.column('cpu').t[1])
  })

  it('appends live points and ignores duplicates and out of order events', () => {
    const b = new SeriesBuffer(3_600_000, 0)
    b.setHistory([at(0), at(5)])
    expect(b.append(at(5))).toBe(false)
    expect(b.append(at(2))).toBe(false)
    expect(b.append(at(10, { memoryBytes: null }))).toBe(true)
    expect(b.column('mem').values).toEqual([100, 100, null])
  })

  it('skips live points denser than the minimum gap', () => {
    const b = new SeriesBuffer(3_600_000, 6_000)
    b.setHistory([at(0)])
    expect(b.append(at(5))).toBe(false)
    expect(b.append(at(6))).toBe(true)
  })

  it('trims points older than the window', () => {
    const b = new SeriesBuffer(10_000, 0)
    b.setHistory([at(0), at(5)])
    b.append(at(20))
    expect(b.length).toBe(1)
  })

  it('keeps live points newer than a refetched history and drops the rest', () => {
    const b = new SeriesBuffer(3_600_000, 0)
    b.append(at(8))
    b.append(at(12))
    b.setHistory([at(0), at(10)])
    expect(b.snapshot().map((p) => p.t % 60)).toEqual([0, 10, 12])
  })

  it('rejects unparseable times and notifies subscribers on change', () => {
    const b = new SeriesBuffer(3_600_000, 0)
    const cb = vi.fn()
    const off = b.subscribe(cb)
    expect(b.append({ ...at(0), takenAt: 'nope' })).toBe(false)
    expect(cb).not.toHaveBeenCalled()
    b.append(at(1))
    expect(cb).toHaveBeenCalledTimes(1)
    off()
    b.append(at(2))
    expect(cb).toHaveBeenCalledTimes(1)
  })
})
