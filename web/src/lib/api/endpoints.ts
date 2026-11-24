import { request } from './client'
import type {
  ActionList,
  ConfigUpdate,
  CreateProfileRequest,
  CreateNetworkRequest,
  CreateVolumeRequest,
  DockerNetwork,
  ImageDetail,
  ImagePruneResult,
  ImageRemoveResult,
  TagImageRequest,
  DockerVolume,
  ConfigUpdateResult,
  ContainerList,
  ContainerLogs,
  EngineInfo,
  SshConfig,
  ImageList,
  Job,
  JobRef,
  PullImageAccepted,
  Metrics,
  MetricsParams,
  NetworkList,
  Profile,
  ProfileConfig,
  ProfileCreated,
  ProfileList,
  NetworkDetail,
  NetworkPruneResult,
  VolumeDetail,
  VolumeList,
  VolumePruneResult,
} from './types'

const enc = encodeURIComponent

/** The fixed text `POST .../volumes/prune` requires in `confirm` (docs/api.md 3.21). */
export const PRUNE_VOLUMES_CONFIRMATION = 'delete-unused-volumes'

/** The fixed text `POST .../networks/prune` requires in `confirm` (docs/api.md 3.25). */
export const PRUNE_NETWORKS_CONFIRMATION = 'delete-unused-networks'

/** The fixed text `POST .../images/prune` requires in `confirm` (docs/api.md 3.29). */
export const PRUNE_IMAGES_CONFIRMATION = 'delete-unused-images'

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
  createProfile: (body: CreateProfileRequest) =>
    request<ProfileCreated>('POST', '/api/profiles', { body }),
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
  getContainerLogs: (name: string, id: string, tail?: number, signal?: AbortSignal) =>
    request<ContainerLogs>(
      'GET',
      withQuery(`/api/profiles/${enc(name)}/containers/${enc(id)}/logs`, { tail }),
      { signal },
    ),
  getSshConfig: (name: string, signal?: AbortSignal) =>
    request<SshConfig>('GET', `/api/profiles/${enc(name)}/ssh-config`, { signal }),
  getEngine: (name: string, signal?: AbortSignal) =>
    request<EngineInfo>('GET', `/api/profiles/${enc(name)}/engine`, { signal }),
  getImages: (name: string, signal?: AbortSignal) =>
    request<ImageList>('GET', `/api/profiles/${enc(name)}/images`, { signal }),
  /**
   * Starts a pull as a job (202) and does not wait for it: follow `jobId` with
   * getJob, cancelJob and the job events like a start or stop. The reference
   * needs a tag or a digest (422 `details.fields.reference` otherwise) and only
   * public registries work. 409 `conflict` when the profile already runs 2
   * pulls or this reference is already being pulled.
   */
  pullImage: (name: string, reference: string) =>
    request<PullImageAccepted>('POST', `/api/profiles/${enc(name)}/images/pull`, {
      body: { reference },
    }),
  /** Adds a tag. 204 on success; 409 when the tag is already on another image. */
  tagImage: (name: string, image: string, body: TagImageRequest) =>
    request<void>('POST', `/api/profiles/${enc(name)}/images/${enc(image)}/tag`, {
      body,
      expectEmpty: true,
    }),
  /** A read: ask for it when the user opens an image, do not poll it. */
  getImage: (name: string, image: string, signal?: AbortSignal) =>
    request<ImageDetail>('GET', `/api/profiles/${enc(name)}/images/${enc(image)}`, { signal }),
  /**
   * Never forced. An id removes the image and all its tags; a tag reference
   * untags. An image used by any container answers 409 (`details.containers`).
   */
  removeImage: (name: string, image: string) =>
    request<ImageRemoveResult>('DELETE', `/api/profiles/${enc(name)}/images/${enc(image)}`),
  /**
   * Irreversible. `all` false removes dangling images only, true every unused
   * image. The server requires the confirmation text, which is sent here so no
   * caller can forget it: call this only after the user has confirmed.
   */
  pruneImages: (name: string, all = false) =>
    request<ImagePruneResult>('POST', `/api/profiles/${enc(name)}/images/prune`, {
      body: { confirm: PRUNE_IMAGES_CONFIRMATION, all },
    }),
  getVolumes: (name: string, signal?: AbortSignal) =>
    request<VolumeList>('GET', `/api/profiles/${enc(name)}/volumes`, { signal }),
  createVolume: (name: string, body: CreateVolumeRequest) =>
    request<DockerVolume>('POST', `/api/profiles/${enc(name)}/volumes`, { body }),
  getVolume: (name: string, volume: string, signal?: AbortSignal) =>
    request<VolumeDetail>('GET', `/api/profiles/${enc(name)}/volumes/${enc(volume)}`, { signal }),
  /** Never forced: a volume still mounted answers 409 with `details.containers`. */
  removeVolume: (name: string, volume: string) =>
    request<void>('DELETE', `/api/profiles/${enc(name)}/volumes/${enc(volume)}`, {
      expectEmpty: true,
    }),
  /**
   * Irreversible. Removes every unused volume, named ones included. The server
   * requires the confirmation text, which is sent here so no caller can forget
   * it: call this only after the user has confirmed.
   */
  pruneVolumes: (name: string) =>
    request<VolumePruneResult>('POST', `/api/profiles/${enc(name)}/volumes/prune`, {
      body: { confirm: PRUNE_VOLUMES_CONFIRMATION },
    }),
  getNetworks: (name: string, signal?: AbortSignal) =>
    request<NetworkList>('GET', `/api/profiles/${enc(name)}/networks`, { signal }),
  createNetwork: (name: string, body: CreateNetworkRequest) =>
    request<DockerNetwork>('POST', `/api/profiles/${enc(name)}/networks`, { body }),
  getNetwork: (name: string, network: string, signal?: AbortSignal) =>
    request<NetworkDetail>('GET', `/api/profiles/${enc(name)}/networks/${enc(network)}`, { signal }),
  /**
   * Never forced: the predefined networks and a network with containers answer
   * 409 (`details.containers` counts them).
   */
  removeNetwork: (name: string, network: string) =>
    request<void>('DELETE', `/api/profiles/${enc(name)}/networks/${enc(network)}`, {
      expectEmpty: true,
    }),
  /**
   * Irreversible. Removes every unused network. The server requires the
   * confirmation text, which is sent here so no caller can forget it: call this
   * only after the user has confirmed.
   */
  pruneNetworks: (name: string) =>
    request<NetworkPruneResult>('POST', `/api/profiles/${enc(name)}/networks/prune`, {
      body: { confirm: PRUNE_NETWORKS_CONFIRMATION },
    }),
  getActions: (name: string, limit?: number, signal?: AbortSignal) =>
    request<ActionList>('GET', withQuery(`/api/profiles/${enc(name)}/actions`, { limit }), {
      signal,
    }),
}
