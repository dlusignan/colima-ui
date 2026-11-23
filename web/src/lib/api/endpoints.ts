import { request } from './client'
import type {
  ActionList,
  ConfigUpdate,
  ConfigUpdateResult,
  ContainerList,
  EngineInfo,
  ImageList,
  Job,
  JobRef,
  Metrics,
  MetricsParams,
  NetworkList,
  Profile,
  ProfileConfig,
  ProfileList,
  VolumeList,
} from './types'

const enc = encodeURIComponent

function withQuery(
  path: string,
  params: Record<string, string | number | undefined>,
): string {
  const q = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined) q.set(k, String(v))
  }
  const s = q.toString()
  return s ? `${path}?${s}` : path
}

export const api = {
  listProfiles: (signal?: AbortSignal) =>
    request<ProfileList>('GET', '/api/profiles', { signal }),
  getProfile: (name: string, signal?: AbortSignal) =>
    request<Profile>('GET', `/api/profiles/${enc(name)}`, { signal }),
  startProfile: (name: string) =>
    request<JobRef>('POST', `/api/profiles/${enc(name)}/start`),
  stopProfile: (name: string) =>
    request<JobRef>('POST', `/api/profiles/${enc(name)}/stop`),
  restartProfile: (name: string) =>
    request<JobRef>('POST', `/api/profiles/${enc(name)}/restart`),
  getJob: (id: string, signal?: AbortSignal) =>
    request<Job>('GET', `/api/jobs/${enc(id)}`, { signal }),
  cancelJob: (id: string) => request<Job>('POST', `/api/jobs/${enc(id)}/cancel`),
  getConfig: (name: string, signal?: AbortSignal) =>
    request<ProfileConfig>('GET', `/api/profiles/${enc(name)}/config`, { signal }),
  updateConfig: (name: string, update: ConfigUpdate) =>
    request<ConfigUpdateResult>('PUT', `/api/profiles/${enc(name)}/config`, {
      body: update,
    }),
  getMetrics: (name: string, params: MetricsParams = {}, signal?: AbortSignal) =>
    request<Metrics>(
      'GET',
      withQuery(`/api/profiles/${enc(name)}/metrics`, { ...params }),
      { signal },
    ),
  getContainers: (name: string, signal?: AbortSignal) =>
    request<ContainerList>('GET', `/api/profiles/${enc(name)}/containers`, { signal }),
  getEngine: (name: string, signal?: AbortSignal) =>
    request<EngineInfo>('GET', `/api/profiles/${enc(name)}/engine`, { signal }),
  getImages: (name: string, signal?: AbortSignal) =>
    request<ImageList>('GET', `/api/profiles/${enc(name)}/images`, { signal }),
  getVolumes: (name: string, signal?: AbortSignal) =>
    request<VolumeList>('GET', `/api/profiles/${enc(name)}/volumes`, { signal }),
  getNetworks: (name: string, signal?: AbortSignal) =>
    request<NetworkList>('GET', `/api/profiles/${enc(name)}/networks`, { signal }),
  getActions: (name: string, limit?: number, signal?: AbortSignal) =>
    request<ActionList>('GET', withQuery(`/api/profiles/${enc(name)}/actions`, { limit }), {
      signal,
    }),
}
