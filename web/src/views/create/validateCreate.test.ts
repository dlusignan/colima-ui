import { describe, expect, it } from 'vitest'
import { buildRequest, EMPTY_VALUES, isReservedName, validateCreate, type CreateValues } from './validateCreate'

const v = (o: Partial<CreateValues>): CreateValues => ({ ...EMPTY_VALUES, name: 'synthetic-a', ...o })

describe('validateCreate', () => {
  it('accepts a bare valid name', () => {
    expect(validateCreate(v({}))).toEqual({})
  })

  it.each(['', '  ', '-a', '.a', 'a b', 'a/b', 'a..b', 'a'.repeat(65)])('rejects the name %j', (name) => {
    expect(validateCreate(v({ name })).name).toBeTruthy()
  })

  it.each(['colima', 'Colima', 'COLIMA-dev', 'colima-'])('rejects the reserved name %s', (name) => {
    expect(isReservedName(name)).toBe(true)
    expect(validateCreate(v({ name })).name).toMatch(/reserved/)
  })

  it.each(['colimadev', 'my-colima', 'default'])('allows %s', (name) => {
    expect(validateCreate(v({ name }))).toEqual({})
  })

  it('rejects non-positive and fractional whole-number fields', () => {
    const e = validateCreate(v({ cpus: '0', diskGiB: '-5', rootDiskGiB: '1.5', memoryGiB: 'x' }))
    expect(Object.keys(e).sort()).toEqual(['cpus', 'diskGiB', 'memoryGiB', 'rootDiskGiB'])
  })

  it('allows fractional memory and caps the maxima', () => {
    expect(validateCreate(v({ memoryGiB: '0.5' })).memoryGiB).toBeTruthy()
    expect(validateCreate(v({ memoryGiB: '1.5' }))).toEqual({})
    expect(validateCreate(v({ memoryGiB: '257' })).memoryGiB).toBeTruthy()
    expect(validateCreate(v({ diskGiB: '8193' })).diskGiB).toBeTruthy()
  })

  it('checks the Kubernetes version only when Kubernetes is on', () => {
    expect(validateCreate(v({ kubernetesVersion: 'junk' }))).toEqual({})
    expect(validateCreate(v({ kubernetes: true, kubernetesVersion: 'junk' })).kubernetesVersion).toBeTruthy()
    expect(validateCreate(v({ kubernetes: true, kubernetesVersion: 'v1.31.2+k3s1' }))).toEqual({})
  })
})

describe('buildRequest', () => {
  it('sends only the name for an untouched form', () => {
    expect(JSON.stringify(buildRequest(v({ name: ' dev ' })))).toBe('{"name":"dev"}')
  })

  it('includes what was filled in and drops the version without Kubernetes', () => {
    expect(
      buildRequest(v({ cpus: '4', memoryGiB: '8', runtime: 'incus', kubernetes: false, kubernetesVersion: 'v1.31.2' })),
    ).toEqual({ name: 'synthetic-a', cpus: 4, memoryGiB: 8, runtime: 'incus' })
  })
})
