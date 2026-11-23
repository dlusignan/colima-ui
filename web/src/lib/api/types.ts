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

export type JobAction = 'start' | 'stop' | 'restart'
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
