import type { MetricsParams } from './types'

/** Query keys shared by views and the SSE invalidations. */
export const queryKeys = {
  profiles: ['profiles'] as const,
  profile: (name: string) => ['profiles', name] as const,
  config: (name: string) => ['profiles', name, 'config'] as const,
  job: (id: string) => ['jobs', id] as const,
  metrics: (name: string, params: MetricsParams = {}) =>
    ['profiles', name, 'metrics', params] as const,
  /** History for one range preset; shares the ['profiles', name, 'metrics'] prefix. */
  metricsRange: (name: string, scope: string, rangeId: string) =>
    ['profiles', name, 'metrics', scope, rangeId] as const,
  containers: (name: string) => ['profiles', name, 'containers'] as const,
  images: (name: string) => ['profiles', name, 'images'] as const,
  volumes: (name: string) => ['profiles', name, 'volumes'] as const,
  networks: (name: string) => ['profiles', name, 'networks'] as const,
  actions: (name: string, limit?: number) =>
    ['profiles', name, 'actions', limit] as const,
}
