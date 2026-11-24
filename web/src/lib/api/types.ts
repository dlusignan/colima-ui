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
/** What a job can be doing: a profile action, or creating the profile (POST /api/profiles, 3.17). */
export type JobAction = ProfileAction | 'create'
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
