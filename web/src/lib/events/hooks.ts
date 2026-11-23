import { useCallback, useEffect, useRef, useSyncExternalStore } from 'react'
import { useEventStreamContext } from './eventStreamContext'
import { EMPTY_JOB } from './stores'
import type {
  ConnectionState,
  EventFilter,
  EventMap,
  JobFinishedEvent,
  JobLine,
  MetricEvent,
} from './types'

export function useConnectionState(): ConnectionState {
  const { client } = useEventStreamContext()
  return useSyncExternalStore(client.onStateChange.bind(client), () => client.getState())
}

/** Rises on every reconnect. Use it as a "refetch after reconnect" dependency. */
export function useConnectionEpoch(): number {
  const { client } = useEventStreamContext()
  return useSyncExternalStore(client.onStateChange.bind(client), () => client.getEpoch())
}

/** Calls the listener for each event. Sets no state, so it causes no re-render. */
export function useEventListener<K extends keyof EventMap>(
  type: K,
  listener: (event: EventMap[K]) => void,
  filter?: EventFilter,
): void {
  const { client } = useEventStreamContext()
  const ref = useRef(listener)
  useEffect(() => {
    ref.current = listener
  })
  const profile = filter?.profile
  const jobId = filter?.jobId
  const scope = filter?.scope
  useEffect(
    () => client.subscribe(type, (e) => ref.current(e), { profile, jobId, scope }),
    [client, type, profile, jobId, scope],
  )
}

export function useLatestMetric(profile: string, scope = 'vm'): MetricEvent | undefined {
  const { metrics } = useEventStreamContext()
  return useSyncExternalStore(metrics.subscribe, () => metrics.getSnapshot(profile, scope))
}

export interface JobOutputResult {
  lines: readonly JobLine[]
  lastSeq: number
  finished: JobFinishedEvent | null
  /** Merges lines from GET /api/jobs/{id}, deduplicated by seq. */
  seed(restOutput: readonly JobLine[]): void
}

export function useJobOutput(jobId: string | undefined): JobOutputResult {
  const { jobs } = useEventStreamContext()
  const subscribe = useCallback(
    (cb: () => void) => {
      const off = jobId ? jobs.subscribe(cb) : () => {}
      const release = jobId ? jobs.acquire(jobId) : () => {}
      return () => {
        off()
        release()
      }
    },
    [jobs, jobId],
  )
  const snap = useSyncExternalStore(subscribe, () => (jobId ? jobs.getSnapshot(jobId) : EMPTY_JOB))
  const seed = useCallback(
    (rest: readonly JobLine[]) => {
      if (jobId) jobs.seed(jobId, rest)
    },
    [jobs, jobId],
  )
  return {
    lines: snap.lines,
    lastSeq: snap.lastSeq,
    finished: snap.finished,
    seed,
  }
}
