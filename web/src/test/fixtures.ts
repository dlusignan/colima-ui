import type { Job, Profile } from '@/lib/api/types'

export function profileFixture(overrides: Partial<Profile> = {}): Profile {
  return {
    name: 'synthetic-a',
    status: 'running',
    runtime: 'docker',
    cpus: 2,
    memoryBytes: 4 * 1024 ** 3,
    diskBytes: 60 * 1024 ** 3,
    colimaVersion: '0.10.1',
    source: 'both',
    activeJobId: null,
    ...overrides,
  }
}

export function jobFixture(overrides: Partial<Job> = {}): Job {
  return {
    id: 'j_1',
    profile: 'synthetic-a',
    action: 'start',
    state: 'running',
    startedAt: '2026-01-01T00:00:00Z',
    finishedAt: null,
    exitCode: null,
    output: [],
    truncated: false,
    droppedLines: 0,
    ...overrides,
  }
}
