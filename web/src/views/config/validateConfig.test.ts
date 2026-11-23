import { describe, expect, it } from 'vitest'
import { buildUpdate, changedFields, toValues, validateConfig } from './validateConfig'

const loaded = { name: 'synthetic-a', cpu: 2, memoryGiB: 4, diskGiB: 60 }

describe('validateConfig', () => {
  it('accepts the loaded values', () => {
    expect(validateConfig(toValues(loaded), loaded)).toEqual({})
  })
  it('rejects bad cpu, memory and disk', () => {
    const e = validateConfig({ cpu: '0', memoryGiB: '300', diskGiB: '8193' }, loaded)
    expect(Object.keys(e).sort()).toEqual(['cpu', 'diskGiB', 'memoryGiB'])
  })
  it('rejects fractions for cpu and disk, empty and NaN input', () => {
    expect(validateConfig({ cpu: '1.5', memoryGiB: '', diskGiB: '60.5' }, loaded)).toMatchObject({
      cpu: expect.any(String),
      memoryGiB: expect.any(String),
      diskGiB: expect.any(String),
    })
  })
  it('allows fractional memory and rejects a shrinking disk', () => {
    expect(validateConfig({ cpu: '2', memoryGiB: '2.5', diskGiB: '60' }, loaded)).toEqual({})
    expect(validateConfig({ cpu: '2', memoryGiB: '4', diskGiB: '59' }, loaded).diskGiB).toMatch(/grow/)
  })
  it('sends only changed fields, compared numerically', () => {
    const v = { cpu: '4', memoryGiB: '4.0', diskGiB: '60' }
    expect(changedFields(v, loaded)).toEqual(['cpu'])
    expect(buildUpdate(v, loaded)).toEqual({ cpu: 4 })
  })
})
