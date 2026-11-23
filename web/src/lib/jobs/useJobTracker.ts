import { useEffect, useRef, useState } from 'react'
import { useQueries, useQueryClient } from '@tanstack/react-query'
import { ApiError } from '@/lib/api/client'
import { api } from '@/lib/api/endpoints'
import { queryKeys } from '@/lib/api/keys'
import type { JobAction, JobState, Profile } from '@/lib/api/types'

export interface TrackedJob {
  jobId: string
  action: JobAction
  state: JobState
  exitCode: number | null
}

export interface JobTracker {
  /** The latest job per profile name: running, or the held outcome. */
  jobs: ReadonlyMap<string, TrackedJob>
  /** Follow a job id this tab did not start (a 409 profile_busy answer). */
  adopt: (profile: string, jobId: string) => void
  /** Drop a held outcome. */
  dismiss: (profile: string) => void
}

/** One retry for a job_not_found: a profile_busy id can be unreadable for a few ms. */
const NOT_FOUND_RETRY_MS = 500
const RUNNING_POLL_MS = 1_000

/**
 * Follows the latest job per profile: the `activeJobId` on the list plus any
 * adopted id. The followed id stays after the list drops it, so a finished
 * outcome (failed, exit code) remains readable until it is dismissed or a newer
 * job replaces it. Deliberately thin: it reads `queryKeys.job(id)` and
 * refetches the list once when a job ends. The log view uses `useJobLog`,
 * which also reads the event stream.
 */
export function useJobTracker(profiles: readonly Profile[] | undefined): JobTracker {
  const queryClient = useQueryClient()
  // profile -> job id being followed.
  const [followed, setFollowed] = useState<ReadonlyMap<string, string>>(new Map())
  const [dismissed, setDismissed] = useState<ReadonlySet<string>>(new Set())

  // A job id on the list that we do not follow yet replaces the old one.
  // Setting state while rendering is the supported way to derive state from props.
  const fresh = (profiles ?? []).filter(
    (p) => p.activeJobId && p.activeJobId !== followed.get(p.name) && !dismissed.has(p.activeJobId),
  )
  if (fresh.length > 0) {
    const next = new Map(followed)
    for (const p of fresh) next.set(p.name, p.activeJobId as string)
    setFollowed(next)
  }

  const entries = [...followed].filter(([, id]) => !dismissed.has(id))
  const results = useQueries({
    queries: entries.map(([, id]) => ({
      queryKey: queryKeys.job(id),
      queryFn: ({ signal }: { signal: AbortSignal }) => api.getJob(id, signal),
      refetchInterval: (query: { state: { data?: { state: JobState } } }) =>
        query.state.data?.state === 'running' ? RUNNING_POLL_MS : false,
      retry: (count: number, error: unknown) =>
        error instanceof ApiError && error.code === 'job_not_found' && count < 1,
      retryDelay: NOT_FOUND_RETRY_MS,
    })),
  })

  const jobs = new Map<string, TrackedJob>()
  entries.forEach(([profile, id], i) => {
    const job = results[i]?.data
    if (job) {
      jobs.set(profile, { jobId: id, action: job.action, state: job.state, exitCode: job.exitCode })
    }
  })

  // Refetch the list once per job that goes from running to a final state.
  const wasRunning = useRef(new Set<string>())
  const signature = [...jobs.values()].map((j) => `${j.jobId}:${j.state}`).join('|')
  useEffect(() => {
    let ended = false
    for (const part of signature ? signature.split('|') : []) {
      const [id, state] = part.split(':')
      if (state === 'running') wasRunning.current.add(id)
      else if (wasRunning.current.delete(id)) ended = true
    }
    if (ended) void queryClient.invalidateQueries({ queryKey: queryKeys.profiles })
  }, [signature, queryClient])

  return {
    jobs,
    adopt: (profile, jobId) => setFollowed((prev) => new Map(prev).set(profile, jobId)),
    dismiss: (profile) => {
      const id = followed.get(profile)
      if (!id) return
      setDismissed((prev) => new Set(prev).add(id))
      setFollowed((prev) => {
        const next = new Map(prev)
        next.delete(profile)
        return next
      })
    },
  }
}
