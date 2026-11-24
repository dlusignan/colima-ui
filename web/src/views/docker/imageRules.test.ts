import { describe, expect, it } from 'vitest'
import type { DockerImage } from '@/lib/api/types'
import { pruneCandidates, shortImageId, splitReference, validatePullReference, validateRepository } from './imageRules'

const image = (tags: string[], containers: number): DockerImage => ({
  id: 'sha256:' + 'a'.repeat(64),
  tags,
  sizeBytes: 1,
  createdAt: '2026-01-01T00:00:00Z',
  containers,
})

describe('imageRules', () => {
  it('shortens ids', () => {
    expect(shortImageId('sha256:' + 'ab'.repeat(32))).toBe('abababababab')
    expect(shortImageId('abababababab')).toBe('abababababab')
  })

  it('splits a reference at its tag colon, not at a registry port', () => {
    expect(splitReference('app:v1')).toEqual({ repository: 'app', tag: 'v1' })
    expect(splitReference('localhost:5000/app:v1')).toEqual({ repository: 'localhost:5000/app', tag: 'v1' })
    expect(splitReference('localhost:5000/app')).toEqual({ repository: 'localhost:5000/app', tag: '' })
  })

  it('accepts what the server accepts and refuses what it refuses', () => {
    for (const ok of ['app', 'team/app', 'a_b-c/e__f', 'localhost:5000/app', 'ghcr.io/owner/app', 'Reg.Example:5/app', 'localhost/app']) {
      expect(validateRepository(ok), ok).toBeUndefined()
    }
    for (const bad of ['', 'App', 'a//b', '/a', 'a/', 'a:1', 'localhost:5000', 'a@sha256:' + 'a'.repeat(64), 'sha256', 'a'.repeat(64), '-a', 'a b', 'a/B', '[::1]:5000/app', 'a'.repeat(256)]) {
      expect(validateRepository(bad), bad).toBeDefined()
    }
  })

  it('lists dangling as untagged and unused, all unused as unused', () => {
    const list = [image(['a:1'], 0), image([], 0), image([], 1), image(['b:1'], 3)]
    expect(pruneCandidates(list, 'dangling')).toEqual([list[1]])
    expect(pruneCandidates(list, 'all')).toEqual([list[0], list[1]])
  })
})

describe('validatePullReference', () => {
  it.each([
    'postgres:18-alpine',
    'ghcr.io/owner/app:1.2',
    'localhost:5000/team/app:v1',
    'Registry.Example.test:5000/team/app:v1',
    'postgres@sha256:' + 'a'.repeat(64),
    'postgres:18@sha256:' + 'a'.repeat(64),
    'a'.repeat(100) + ':1',
  ])('accepts %s', (ref) => expect(validatePullReference(ref)).toBeUndefined())

  it.each([
    ['empty', '', /Enter an image reference/],
    ['no tag or digest', 'postgres', /Add a tag or a digest/],
    ['registry with port and no tag', 'localhost:5000/app', /Add a tag or a digest/],
    ['too long', 'a'.repeat(254) + ':1', /at most 255/],
    ['userinfo', 'user:pass@registry.example.test/app:1', /Credentials/],
    ['bad digest', 'postgres@sha256:abc', /Not a valid digest/],
    ['sha256 repository', 'sha256:v1', /read like an image ID/],
    ['64 hex repository', 'a'.repeat(64) + ':v1', /read like an image ID/],
    ['uppercase path', 'Team/App:1', /lowercase/],
    ['space', 'post gres:1', /lowercase/],
    ['bad tag', 'app:-x', /Use letters, digits/],
    ['bad host', 'bad_host.example/app:1', /registry host|lowercase/],
    ['ipv6 host', '[::1]:5000/app:1', /registry host|lowercase|Use letters/],
  ])('rejects %s', (_n, ref, message) => expect(validatePullReference(ref)).toMatch(message))
})
