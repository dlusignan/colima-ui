import type { MetricsParams } from './types'

/** Query keys shared by views and the SSE invalidations. */
export const queryKeys = {
  profiles: ['profiles'] as const,
  profile: (name: string) => ['profiles', name] as const,
  config: (name: string) => ['profiles', name, 'config'] as const,
  job: (id: string) => ['jobs', id] as const,
  metrics: (name: string, params: MetricsParams = {}) =>
    ['profiles', name, 'metrics', params] as const,
  containers: (name: string) => ['profiles', name, 'containers'] as const,
  actions: (name: string, limit?: number) =>
    ['profiles', name, 'actions', limit] as const,
}
