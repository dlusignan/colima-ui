import type { JobFinishedEvent, JobOutputEvent, MetricEvent } from '@/lib/api/types'

export type { JobFinishedEvent, JobOutputEvent, MetricEvent }

export type ConnectionState = 'connecting' | 'open' | 'reconnecting' | 'closed'

export interface EventMap {
  'job.output': JobOutputEvent
  'job.finished': JobFinishedEvent
  metric: MetricEvent
}

/** Client side filter. The one connection always carries every profile. */
export interface EventFilter {
  profile?: string
  jobId?: string
  scope?: string
}

export interface JobLine {
  seq: number
  line: string
}

/** What the hooks and stores need from a stream client (the real one or a fake). */
export interface EventStreamSource {
  connect(): void
  close(): void
  getState(): ConnectionState
  /** Number of re-opens after the first open. Rises on every reconnect. */
  getEpoch(): number
  subscribe<K extends keyof EventMap>(
    type: K,
    listener: (event: EventMap[K]) => void,
    filter?: EventFilter,
  ): () => void
  /** Called when the stream opens again after an earlier open. */
  onResync(callback: () => void): () => void
  /** Called on every state or epoch change. */
  onStateChange(callback: () => void): () => void
}
