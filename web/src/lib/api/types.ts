// Response and event types for the REST and SSE contract in docs/api.md.
// This is the only file that declares them. shapes.ts checks them against the
// contract's example payloads.

export type ProfileStatus = 'running' | 'stopped' | 'unknown'
export type ProfileSource = 'both' | 'cli_only' | 'file_only'

export interface ProfileWarning {
  /** colima_unavailable, config_unreadable, config_invalid, colima_version_unsupported; clients must tolerate new codes. */
  code: string
  message: string
}

export interface Profile {
  name: string
  status: ProfileStatus
  runtime: string
  cpus: number
  memoryBytes: number
  diskBytes: number
  colimaVersion: string
  source: ProfileSource
  activeJobId: string | null
  /** Omitted by the server when there are none. */
  warnings?: ProfileWarning[]
}

export interface ProfileList {
  profiles: Profile[]
}

export interface JobRef {
  jobId: string
}

/** Body of POST /api/profiles. Only name is required; a missing setting keeps Colima's default. */
export interface CreateProfileRequest {
  name: string
  cpus?: number
  memoryGiB?: number
  diskGiB?: number
  rootDiskGiB?: number
  runtime?: string
  vmType?: string
  arch?: string
  mountType?: string
  kubernetes?: boolean
  kubernetesVersion?: string
  // Advanced settings, all optional.
  hostname?: string
  dns?: string[]
  dnsHosts?: DnsHost[]
  sshAgent?: boolean
  vzRosetta?: boolean
  portForwarder?: 'ssh' | 'grpc' | 'none'
  mountInotify?: boolean
  networkAddress?: boolean
  networkMode?: 'shared' | 'bridged'
  networkInterface?: string
  cpuType?: string
  /** Host folders shared into the VM. At most 16; every folder is visible to every container. */
  mounts?: Mount[]
  /** Environment variables for the VM. At most 32; values are never echoed back by the server. */
  env?: Record<string, string>
}

/** One host folder to mount (`mounts` in POST /api/profiles). Read-only unless `writable` is true. */
export interface Mount {
  path: string
  writable?: boolean
}

/** One custom DNS name for the VM's resolver (`dnsHosts` in POST /api/profiles). */
export interface DnsHost {
  name: string
  address: string
}

export interface ProfileCreated {
  jobId: string
  profile: string
}

/** What a profile's buttons can start. */
export type ProfileAction = 'start' | 'stop' | 'restart'
/**
 * What a job can be doing: a profile action, creating the profile (POST
 * /api/profiles, 3.17), or pulling an image (3.30; it never holds the profile's
 * start/stop slot, and its output is progress text rather than process output).
 */
export type JobAction = ProfileAction | 'create' | 'image-pull'
export type JobState = 'running' | 'succeeded' | 'failed' | 'cancelled'

export interface JobOutputLine {
  seq: number
  line: string
}

export interface Job {
  id: string
  profile: string
  action: JobAction
  state: JobState
  startedAt: string
  finishedAt: string | null
  exitCode: number | null
  output: JobOutputLine[]
  truncated: boolean
  droppedLines: number
}

export interface ProfileConfig {
  name: string
  cpu: number
  memoryGiB: number
  diskGiB: number
}

/** PUT body: the fields to change; absent fields are left alone. */
export interface ConfigUpdate {
  cpu?: number
  memoryGiB?: number
  diskGiB?: number
}

export interface ConfigUpdateResult {
  config: ProfileConfig
  restartRequired: string[]
}

export interface MetricPoint {
  takenAt: string
  /** null when the sampler could not read the value; never treat it as 0. */
  cpuPercent: number | null
  memoryBytes: number | null
  diskBytes: number | null
}

export interface Metrics {
  profile: string
  scope: string
  from: string
  to: string
  points: MetricPoint[]
}

export interface MetricsParams {
  scope?: string
  from?: string
  to?: string
  maxPoints?: number
}

export interface ContainerPort {
  containerPort: number
  protocol: string
  hostIp: string | null
  hostPort: number | null
}

export interface Container {
  id: string
  name: string
  image: string
  state: string
  cpuPercent: number
  memoryBytes: number
  status: string
  createdAt: string
  ports: ContainerPort[]
}

export interface ContainerList {
  containers: Container[]
}

export interface DockerImage {
  id: string
  tags: string[]
  sizeBytes: number
  createdAt: string
  containers: number
}

export interface ImageList {
  images: DockerImage[]
}

/** Body of `POST /api/profiles/{name}/images/pull` (docs/api.md 3.30). */
export interface PullImageRequest {
  /** A reference with a tag or a digest, such as `postgres:18-alpine`; public registries only. */
  reference: string
}

/** The 202 answer of an image pull: a job to follow like start and stop. */
export interface PullImageAccepted extends JobRef {
  action: 'image-pull'
  profile: string
  state: 'running'
}

/** Body of `POST /api/profiles/{name}/images/{image}/tag`. Both fields are required. */
export interface TagImageRequest {
  /** No tag, no digest; a registry host and path are allowed (docs/api.md 3.26). */
  repository: string
  tag: string
}

/** One layer of an image: time and size only, never the build command. */
export interface ImageLayer {
  createdAt: string | null
  sizeBytes: number
}

/** Answer of `GET /api/profiles/{name}/images/{image}`. No environment, labels or commands. */
export interface ImageDetail {
  id: string
  tags: string[]
  digests: string[]
  sizeBytes: number
  createdAt: string | null
  architecture: string
  os: string
  /** Port numbers only, sorted. */
  exposedPorts: number[]
  containers: number
  containerNames: string[]
  /** At most 200 layers, newest first. */
  layers: ImageLayer[]
  layerCount: number
}

/**
 * Answer of `DELETE /api/profiles/{name}/images/{image}`. When Docker acted but
 * its answer was unreadable, `resultComplete` is false and `removedCount` is
 * null. `truncated` is true when the lists hold fewer entries than
 * `removedCount`.
 */
export interface ImageRemoveResult {
  untagged: string[]
  /** 12-character image ids. */
  deleted: string[]
  removedCount: number | null
  truncated: boolean
  resultComplete: boolean
}

/** Body of `POST /api/profiles/{name}/images/prune`. */
export interface PruneImagesRequest {
  confirm: string
  /** false (the default): dangling images only. true: every unused image, tagged ones included. */
  all?: boolean
}

/**
 * Answer of `POST /api/profiles/{name}/images/prune`. Irreversible. When Docker
 * ran the prune but its answer was unreadable, `resultComplete` is false and
 * `removedCount` and `reclaimedBytes` may be null: the images are gone, only
 * the figures are unknown.
 */
export interface ImagePruneResult {
  untagged: string[]
  deleted: string[]
  removedCount: number | null
  truncated: boolean
  reclaimedBytes: number | null
  all: boolean
  resultComplete: boolean
}

export interface ContainerLogs {
  /** Oldest first, each line starts with Docker's timestamp. Plain text only. */
  lines: string[]
  truncated: boolean
}

export interface DockerVolume {
  name: string
  driver: string
  createdAt: string
  sizeBytes: number | null
  containers: number | null
}

export interface VolumeList {
  volumes: DockerVolume[]
}

export interface DockerNetwork {
  id: string
  name: string
  driver: string
  scope: string
  internal: boolean
  containers: number
}

/** Body of `POST /api/profiles/{name}/volumes`. */
export interface CreateVolumeRequest {
  name: string
  /** At most 16 pairs; sent only when not empty. */
  labels?: Record<string, string>
}

/** Answer of `GET /api/profiles/{name}/volumes/{volume}`. No host path. */
export interface VolumeDetail {
  name: string
  driver: string
  createdAt: string
  sizeBytes: number | null
  labels: Record<string, string>
  containers: number
  containerNames: string[]
}

/**
 * Answer of `POST /api/profiles/{name}/volumes/prune`. On the pinned Docker API
 * v1.41 every unused volume is removed, named ones included, and
 * `includesNamedVolumes` says so. Irreversible. When Docker ran the prune but
 * its answer was unreadable, `resultComplete` is false and `removedCount` and
 * `reclaimedBytes` are null: the volumes are gone, only the figures are unknown.
 * `truncated` is true when `removed` lists fewer names than `removedCount`.
 */
export interface VolumePruneResult {
  removed: string[]
  removedCount: number | null
  truncated: boolean
  reclaimedBytes: number | null
  includesNamedVolumes: boolean
  resultComplete: boolean
}

/** Body of `POST /api/profiles/{name}/volumes/prune`. */
export interface PruneVolumesRequest {
  confirm: string
}

/** Body of `POST /api/profiles/{name}/networks`. Bridge networks only. */
export interface CreateNetworkRequest {
  name: string
  internal?: boolean
  /** One CIDR (IPv4 /8 to /28, IPv6 /48 to /120); host bits are ignored. */
  subnet?: string
}

export interface NetworkSubnet {
  subnet: string
  /** Empty when the network has no gateway. */
  gateway: string
}

/** A container attached to a network; addresses are empty when unknown. */
export interface NetworkContainer {
  name: string
  ipv4Address: string
  ipv6Address: string
}

/** Answer of `GET /api/profiles/{name}/networks/{network}`. */
export interface NetworkDetail {
  id: string
  name: string
  driver: string
  scope: string
  internal: boolean
  subnets: NetworkSubnet[]
  containers: NetworkContainer[]
}

/**
 * Answer of `POST /api/profiles/{name}/networks/prune`. Irreversible. When
 * Docker ran the prune but its answer was unreadable, `resultComplete` is
 * false and `removedCount` is null: the networks are gone, only the list is
 * unknown. `truncated` is true when `removed` lists fewer names than
 * `removedCount`.
 */
export interface NetworkPruneResult {
  removed: string[]
  removedCount: number | null
  truncated: boolean
  resultComplete: boolean
}

/** Body of `POST /api/profiles/{name}/networks/prune`. */
export interface PruneNetworksRequest {
  confirm: string
}

export interface NetworkList {
  networks: DockerNetwork[]
}

export interface EngineContainers {
  total: number
  running: number
  paused: number
  stopped: number
}

export interface EngineDiskUsage {
  imagesBytes: number
  containersBytes: number
  volumesBytes: number
  buildCacheBytes: number
  layersBytes: number
}

export interface EngineInfo {
  version: string
  apiVersion: string
  os: string
  arch: string
  cpus: number
  memoryBytes: number
  containers: EngineContainers
  images: number
  diskUsage: EngineDiskUsage
}

export type ActionOutcome = 'success' | 'failed' | 'cancelled'

export interface ActionEntry {
  startedAt: string
  /** start, stop, restart, config_change; clients must tolerate new values. */
  action: string
  outcome: ActionOutcome
  detail: string | null
}

export interface ActionList {
  entries: ActionEntry[]
}

export interface Health {
  status: string
}

export interface ApiErrorBody {
  error: {
    code: string
    message: string
    details: Record<string, unknown>
  }
}

// Server Sent Events payloads (docs/api.md section 5.3).

export interface JobOutputEvent {
  jobId: string
  profile: string
  seq: number
  line: string
}

export interface JobFinishedEvent {
  jobId: string
  profile: string
  state: 'succeeded' | 'failed' | 'cancelled'
  finishedAt: string
  exitCode: number | null
}

export interface MetricEvent {
  profile: string
  scope: string
  takenAt: string
  cpuPercent: number
  memoryBytes: number
  diskBytes: number | null
}
