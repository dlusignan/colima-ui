// Field tables that mirror the types in types.ts. `satisfies Record<keyof T,
// Kind>` makes `tsc -b` fail when a field is added to or removed from a type
// without updating its table. contract.test.ts then checks the tables against
// the example payloads in docs/api.md.

import type {
  ActionEntry,
  ActionList,
  ApiErrorBody,
  Container,
  ContainerLogs,
  ContainerPort,
  ContainerList,
  DockerImage,
  DockerNetwork,
  DockerVolume,
  EngineContainers,
  EngineDiskUsage,
  EngineInfo,
  ImageList,
  NetworkList,
  VolumeList,
  ConfigUpdate,
  CreateProfileRequest,
  ConfigUpdateResult,
  Health,
  Job,
  JobFinishedEvent,
  JobOutputEvent,
  JobOutputLine,
  JobRef,
  MetricEvent,
  MetricPoint,
  Metrics,
  Profile,
  ProfileConfig,
  ProfileCreated,
  ProfileList,
  ProfileWarning,
} from './types'

export type Shape = { readonly [field: string]: Kind }

export type Kind =
  | 'string'
  | 'number'
  | 'boolean'
  | 'string|null'
  | 'number|null'
  | 'string[]'
  | 'record'
  | { readonly arrayOf: Shape }
  /** Like arrayOf, but the key may be absent. */
  | { readonly optionalArrayOf: Shape }
  | { readonly shape: Shape }

export const profileWarningShape = {
  code: 'string',
  message: 'string',
} as const satisfies Record<keyof ProfileWarning, Kind>

export const profileShape = {
  name: 'string',
  status: 'string',
  runtime: 'string',
  cpus: 'number',
  memoryBytes: 'number',
  diskBytes: 'number',
  colimaVersion: 'string',
  source: 'string',
  activeJobId: 'string|null',
  warnings: { optionalArrayOf: profileWarningShape },
} as const satisfies Record<keyof Profile, Kind>

export const profileListShape = {
  profiles: { arrayOf: profileShape },
} as const satisfies Record<keyof ProfileList, Kind>

export const jobRefShape = {
  jobId: 'string',
} as const satisfies Record<keyof JobRef, Kind>

export const createProfileRequestShape = {
  name: 'string',
  cpus: 'number',
  memoryGiB: 'number',
  diskGiB: 'number',
  rootDiskGiB: 'number',
  runtime: 'string',
  vmType: 'string',
  arch: 'string',
  mountType: 'string',
  kubernetes: 'boolean',
  kubernetesVersion: 'string',
} as const satisfies Record<keyof CreateProfileRequest, Kind>

export const profileCreatedShape = {
  jobId: 'string',
  profile: 'string',
} as const satisfies Record<keyof ProfileCreated, Kind>

export const jobOutputLineShape = {
  seq: 'number',
  line: 'string',
} as const satisfies Record<keyof JobOutputLine, Kind>

export const jobShape = {
  id: 'string',
  profile: 'string',
  action: 'string',
  state: 'string',
  startedAt: 'string',
  finishedAt: 'string|null',
  exitCode: 'number|null',
  output: { arrayOf: jobOutputLineShape },
  truncated: 'boolean',
  droppedLines: 'number',
} as const satisfies Record<keyof Job, Kind>

export const configShape = {
  name: 'string',
  cpu: 'number',
  memoryGiB: 'number',
  diskGiB: 'number',
} as const satisfies Record<keyof ProfileConfig, Kind>

export const configUpdateShape = {
  cpu: 'number',
  memoryGiB: 'number',
  diskGiB: 'number',
} as const satisfies Record<keyof ConfigUpdate, Kind>

export const configUpdateResultShape = {
  config: { shape: configShape },
  restartRequired: 'string[]',
} as const satisfies Record<keyof ConfigUpdateResult, Kind>

export const metricPointShape = {
  takenAt: 'string',
  cpuPercent: 'number|null',
  memoryBytes: 'number|null',
  diskBytes: 'number|null',
} as const satisfies Record<keyof MetricPoint, Kind>

export const metricsShape = {
  profile: 'string',
  scope: 'string',
  from: 'string',
  to: 'string',
  points: { arrayOf: metricPointShape },
} as const satisfies Record<keyof Metrics, Kind>

export const containerPortShape = {
  containerPort: 'number',
  protocol: 'string',
  hostIp: 'string|null',
  hostPort: 'number|null',
} as const satisfies Record<keyof ContainerPort, Kind>

export const containerShape = {
  id: 'string',
  name: 'string',
  image: 'string',
  state: 'string',
  cpuPercent: 'number',
  memoryBytes: 'number',
  status: 'string',
  createdAt: 'string',
  ports: { arrayOf: containerPortShape },
} as const satisfies Record<keyof Container, Kind>

export const containerListShape = {
  containers: { arrayOf: containerShape },
} as const satisfies Record<keyof ContainerList, Kind>

export const dockerImageShape = {
  id: 'string',
  tags: 'string[]',
  sizeBytes: 'number',
  createdAt: 'string',
  containers: 'number',
} as const satisfies Record<keyof DockerImage, Kind>

export const imageListShape = {
  images: { arrayOf: dockerImageShape },
} as const satisfies Record<keyof ImageList, Kind>

export const dockerVolumeShape = {
  name: 'string',
  driver: 'string',
  createdAt: 'string',
  sizeBytes: 'number|null',
  containers: 'number|null',
} as const satisfies Record<keyof DockerVolume, Kind>

export const volumeListShape = {
  volumes: { arrayOf: dockerVolumeShape },
} as const satisfies Record<keyof VolumeList, Kind>

export const dockerNetworkShape = {
  id: 'string',
  name: 'string',
  driver: 'string',
  scope: 'string',
  internal: 'boolean',
  containers: 'number',
} as const satisfies Record<keyof DockerNetwork, Kind>

export const networkListShape = {
  networks: { arrayOf: dockerNetworkShape },
} as const satisfies Record<keyof NetworkList, Kind>

export const engineContainersShape = {
  total: 'number',
  running: 'number',
  paused: 'number',
  stopped: 'number',
} as const satisfies Record<keyof EngineContainers, Kind>

export const engineDiskUsageShape = {
  imagesBytes: 'number',
  containersBytes: 'number',
  volumesBytes: 'number',
  buildCacheBytes: 'number',
  layersBytes: 'number',
} as const satisfies Record<keyof EngineDiskUsage, Kind>

export const engineInfoShape = {
  version: 'string',
  apiVersion: 'string',
  os: 'string',
  arch: 'string',
  cpus: 'number',
  memoryBytes: 'number',
  containers: { shape: engineContainersShape },
  images: 'number',
  diskUsage: { shape: engineDiskUsageShape },
} as const satisfies Record<keyof EngineInfo, Kind>

export const containerLogsShape = {
  lines: 'string[]',
  truncated: 'boolean',
} as const satisfies Record<keyof ContainerLogs, Kind>

export const actionEntryShape = {
  startedAt: 'string',
  action: 'string',
  outcome: 'string',
  detail: 'string|null',
} as const satisfies Record<keyof ActionEntry, Kind>

export const actionListShape = {
  entries: { arrayOf: actionEntryShape },
} as const satisfies Record<keyof ActionList, Kind>

export const healthShape = {
  status: 'string',
} as const satisfies Record<keyof Health, Kind>

export const apiErrorShape = {
  error: {
    shape: { code: 'string', message: 'string', details: 'record' },
  },
} as const satisfies Record<keyof ApiErrorBody, Kind>

export const jobOutputEventShape = {
  jobId: 'string',
  profile: 'string',
  seq: 'number',
  line: 'string',
} as const satisfies Record<keyof JobOutputEvent, Kind>

export const jobFinishedEventShape = {
  jobId: 'string',
  profile: 'string',
  state: 'string',
  finishedAt: 'string',
  exitCode: 'number|null',
} as const satisfies Record<keyof JobFinishedEvent, Kind>

export const metricEventShape = {
  profile: 'string',
  scope: 'string',
  takenAt: 'string',
  cpuPercent: 'number',
  memoryBytes: 'number',
  diskBytes: 'number|null',
} as const satisfies Record<keyof MetricEvent, Kind>
