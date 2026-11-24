import { readFileSync } from 'node:fs'
import path from 'node:path'
import { describe, expect, it } from 'vitest'
import * as s from './shapes'
import type { Kind, Shape } from './shapes'

// Extracts the fenced json blocks from docs/api.md, attaches each to the
// nearest preceding heading and checks it against the field tables in
// shapes.ts. A json block that is not mapped below fails the test, so a
// contract edit cannot go unchecked.

const docPath = path.resolve(import.meta.dirname, '../../../../docs/api.md')
const doc = readFileSync(docPath, 'utf8')

interface Block {
  key: string
  heading: string
  body: unknown
}

function extractBlocks(markdown: string): Block[] {
  const blocks: Block[] = []
  const counts = new Map<string, number>()
  let heading = ''
  let num = ''
  let inJson = false
  let inOther = false
  let buf: string[] = []
  for (const line of markdown.split('\n')) {
    if (inJson) {
      if (line.startsWith('```')) {
        const idx = counts.get(num) ?? 0
        counts.set(num, idx + 1)
        blocks.push({ key: `${num}#${idx}`, heading, body: JSON.parse(buf.join('\n')) })
        inJson = false
        buf = []
      } else buf.push(line)
      continue
    }
    if (inOther) {
      if (line.startsWith('```')) inOther = false
      continue
    }
    const h = /^#{2,3} (\d+(?:\.\d+)?)\.? (.*)$/.exec(line)
    if (h) {
      num = h[1]
      heading = line
      continue
    }
    if (line.startsWith('```json')) inJson = true
    else if (line.startsWith('```')) inOther = true
  }
  return blocks
}

function checkKind(kind: Kind, value: unknown, where: string): void {
  switch (kind) {
    case 'string':
    case 'number':
    case 'boolean':
      expect(typeof value, where).toBe(kind)
      return
    case 'string|null':
      expect(value === null || typeof value === 'string', where).toBe(true)
      return
    case 'number|null':
      expect(value === null || typeof value === 'number', where).toBe(true)
      return
    case 'number[]':
      expect(Array.isArray(value), where).toBe(true)
      for (const v of value as unknown[]) expect(typeof v, where).toBe('number')
      return
    case 'string[]':
      expect(Array.isArray(value), where).toBe(true)
      for (const v of value as unknown[]) expect(typeof v, where).toBe('string')
      return
    case 'record':
      expect(typeof value === 'object' && value !== null && !Array.isArray(value), where).toBe(true)
      return
  }
  const element = 'arrayOf' in kind ? kind.arrayOf : 'optionalArrayOf' in kind ? kind.optionalArrayOf : undefined
  if (element) {
    expect(Array.isArray(value), where).toBe(true)
    ;(value as unknown[]).forEach((v, i) => checkShape(element, v, `${where}[${i}]`))
    return
  }
  if ('shape' in kind) checkShape(kind.shape, value, where)
}

function checkShape(shape: Shape, value: unknown, where: string, partial = false): void {
  expect(typeof value === 'object' && value !== null && !Array.isArray(value), where).toBe(true)
  const obj = value as Record<string, unknown>
  const known = Object.keys(shape).sort()
  // Keys declared optional (the server omits them) may be absent.
  const required = known.filter((k) => {
    const kind = shape[k]
    return typeof kind === 'string' || !('optionalArrayOf' in kind)
  })
  const actual = Object.keys(obj).sort()
  if (partial) {
    for (const k of actual) expect(known, `${where}: unexpected key ${k}`).toContain(k)
  } else {
    for (const k of actual) expect(known, `${where}: unexpected key ${k}`).toContain(k)
    for (const k of required) expect(actual, `${where}: missing key ${k}`).toContain(k)
  }
  for (const k of actual) checkKind(shape[k], obj[k], `${where}.${k}`)
}

interface Mapping {
  shape: Shape
  partial?: boolean
}

const mapped: Record<string, Mapping> = {
  '2#0': { shape: s.apiErrorShape },
  '3.1#0': { shape: s.healthShape },
  '3.2#0': { shape: s.profileListShape },
  '3.4#0': { shape: s.jobRefShape },
  '3.5#0': { shape: s.jobShape },
  '3.7#0': { shape: s.configShape },
  '3.8#0': { shape: s.configUpdateShape, partial: true },
  '3.8#1': { shape: s.configUpdateResultShape },
  '3.8#2': { shape: s.apiErrorShape },
  '3.9#0': { shape: s.metricsShape },
  '3.10#0': { shape: s.containerListShape },
  '3.12#0': { shape: s.imageListShape },
  '3.11#0': { shape: s.actionListShape },
  '3.26#0': { shape: s.tagImageRequestShape },
  '3.27#0': { shape: s.imageDetailShape },
  '3.28#0': { shape: s.imageRemoveResultShape },
  '3.28#1': { shape: s.imageRemoveResultShape },
  '3.29#0': { shape: s.pruneImagesRequestShape },
  '3.29#1': { shape: s.imagePruneResultShape },
  '3.29#2': { shape: s.imagePruneResultShape },
  '3.32#0': { shape: s.deleteProfileRequestShape, partial: true },
  '3.32#1': { shape: s.jobRefShape },
  '3.33#0': { shape: s.cachePruneRequestShape, partial: true },
  '3.33#1': { shape: s.cachePruneAcceptedShape },
  '3.30#0': { shape: s.pullImageRequestShape },
  '3.30#1': { shape: s.pullImageAcceptedShape },
  '3.13#0': { shape: s.volumeListShape },
  '3.14#0': { shape: s.networkListShape },
  '3.15#0': { shape: s.engineInfoShape },
  '3.16#0': { shape: s.containerLogsShape },
  '3.18#0': { shape: s.createVolumeRequestShape, partial: true },
  '3.18#1': { shape: s.dockerVolumeShape },
  '3.19#0': { shape: s.volumeDetailShape },
  '3.21#0': { shape: s.pruneVolumesRequestShape },
  '3.21#1': { shape: s.volumePruneResultShape },
  '3.21#2': { shape: s.volumePruneResultShape },
  '3.22#0': { shape: s.createNetworkRequestShape, partial: true },
  '3.22#1': { shape: s.dockerNetworkShape },
  '3.23#0': { shape: s.networkDetailShape },
  '3.25#0': { shape: s.pruneNetworksRequestShape },
  '3.25#1': { shape: s.networkPruneResultShape },
  '3.25#2': { shape: s.networkPruneResultShape },
  '3.31#0': { shape: s.sshConfigShape },
  '3.17#0': { shape: s.createProfileRequestShape, partial: true },
  '3.17#1': { shape: s.profileCreatedShape },
  '3.17#2': { shape: s.apiErrorShape },
  '5.3#0': { shape: s.jobOutputEventShape },
  '5.3#1': { shape: s.jobFinishedEventShape },
  '5.3#2': { shape: s.metricEventShape },
}

// Json blocks deliberately not checked. Empty on purpose.
const ignored = new Set<string>()

const blocks = extractBlocks(doc)

describe('docs/api.md contract', () => {
  it('finds the json blocks', () => {
    expect(blocks.length).toBeGreaterThanOrEqual(Object.keys(mapped).length)
  })

  it('has a block for every mapped heading', () => {
    const keys = new Set(blocks.map((b) => b.key))
    for (const k of Object.keys(mapped)) expect(keys.has(k), `missing block ${k}`).toBe(true)
  })

  it('maps every json block (a new unmapped block fails)', () => {
    const unmapped = blocks.filter((b) => !(b.key in mapped) && !ignored.has(b.key))
    expect(unmapped.map((b) => `${b.key} under "${b.heading}"`)).toEqual([])
  })

  for (const b of blocks) {
    const m = mapped[b.key]
    if (!m) continue
    it(`block ${b.key} (${b.heading}) matches its shape`, () => {
      checkShape(m.shape, b.body, b.key, m.partial)
    })
  }

  it('the checker rejects missing and extra keys', () => {
    expect(() => checkShape(s.healthShape, {}, 't')).toThrow()
    expect(() => checkShape(s.healthShape, { status: 'ok', extra: 1 }, 't')).toThrow()
    expect(() => checkShape(s.healthShape, { status: 1 }, 't')).toThrow()
  })

  it('treats warnings as optional but checks them when present', () => {
    const base = {
      name: 'a', status: 'running', runtime: 'docker', cpus: 1, memoryBytes: 1,
      diskBytes: 1, colimaVersion: '', source: 'both', activeJobId: null,
    }
    expect(() => checkShape(s.profileShape, base, 't')).not.toThrow()
    expect(() =>
      checkShape(s.profileShape, { ...base, warnings: [{ code: 'x', message: 'y' }] }, 't'),
    ).not.toThrow()
    expect(() => checkShape(s.profileShape, { ...base, warnings: [{ code: 'x' }] }, 't')).toThrow()
    expect(() => checkShape(s.profileShape, { ...base, warnings: 'x' }, 't')).toThrow()
    const { name, ...noName } = base
    void name
    expect(() => checkShape(s.profileShape, noName, 't')).toThrow()
  })
})
