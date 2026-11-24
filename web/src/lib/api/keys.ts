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
  containerLogs: (name: string, id: string, tail: number) =>
    ['profiles', name, 'container-logs', id, tail] as const,
  sshConfig: (name: string) => ['profiles', name, 'ssh-config'] as const,
  kubernetes: (name: string) => ['profiles', name, 'kubernetes'] as const,
  engine: (name: string) => ['profiles', name, 'engine'] as const,
  images: (name: string) => ['profiles', name, 'images'] as const,
  image: (name: string, image: string) => ['profiles', name, 'images', image] as const,
  volumes: (name: string) => ['profiles', name, 'volumes'] as const,
  volume: (name: string, volume: string) => ['profiles', name, 'volumes', volume] as const,
  networks: (name: string) => ['profiles', name, 'networks'] as const,
  network: (name: string, network: string) => ['profiles', name, 'networks', network] as const,
  actions: (name: string, limit?: number) =>
    ['profiles', name, 'actions', limit] as const,
}
