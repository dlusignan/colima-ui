import { useEffect, useRef, useState } from 'react'
import { useMutation, useQueries, useQueryClient } from '@tanstack/react-query'
import { ApiError } from '@/lib/api/client'
import { api } from '@/lib/api/endpoints'
import { queryKeys } from '@/lib/api/keys'
import type { Job } from '@/lib/api/types'
import { useToast } from '@/lib/toast/useToast'
import { forgetPull, loadPulls, rememberPull, type TrackedPull } from './pullStore'

const POLL_MS = 2_000
const NOT_FOUND_RETRY_MS = 500

export interface RunningPull extends TrackedPull {
  /** The last line of the job output (the newest layer state), or null before there is one. */
  lastLine: string | null
  cancelling: boolean
}

/**
 * Follows the image pulls of one profile. It polls `GET /api/jobs/{id}` every
 * 2 s while a pull runs rather than reading the event stream: the same
 * response carries the output (the progress line) and the state, it needs no
 * stream in the tree, and a missed event cannot leave a stuck row. The outcome
 * toast belongs to JobOutcomeToasts, so this hook shows none for completion.
 * When a pull ends (any state, or the job is gone after a backend restart) the
 * row goes away and the images and engine queries refresh.
 */
export function usePullJobs(profile: string) {
  const queryClient = useQueryClient()
  const toast = useToast()
  const [all, setAll] = useState<TrackedPull[]>(loadPulls)
  const [cancelling, setCancelling] = useState<ReadonlySet<string>>(new Set())
  const mine = all.filter((p) => p.profile === profile)

  const results = useQueries({
    queries: mine.map((p) => ({
      queryKey: queryKeys.job(p.jobId),
      queryFn: ({ signal }: { signal: AbortSignal }) => api.getJob(p.jobId, signal),
      refetchInterval: (query: { state: { data?: Job } }) =>
        query.state.data?.state === 'running' ? POLL_MS : false,
      retry: (count: number, error: unknown) =>
        error instanceof ApiError ? error.code === 'job_not_found' && count < 1 : count < 2,
      retryDelay: NOT_FOUND_RETRY_MS,
    })),
  })

  const running: RunningPull[] = []
  const ended: string[] = []
  mine.forEach((p, i) => {
    const r = results[i]
    const gone = r?.error instanceof ApiError && r.error.code === 'job_not_found'
    if (gone || (r?.data && r.data.state !== 'running')) {
      ended.push(p.jobId)
      return
    }
    const lines = r?.data?.output ?? []
    running.push({
      ...p,
      lastLine: lines.length > 0 ? lines[lines.length - 1].line : null,
      cancelling: cancelling.has(p.jobId),
    })
  })

  const handled = useRef(new Set<string>())
  const endedKey = ended.join('|')
  useEffect(() => {
    let any = false
    for (const id of endedKey ? endedKey.split('|') : []) {
      if (handled.current.has(id)) continue
      handled.current.add(id)
      forgetPull(id)
      any = true
    }
    if (any) {
      void queryClient.invalidateQueries({ queryKey: queryKeys.images(profile) })
      void queryClient.invalidateQueries({ queryKey: queryKeys.engine(profile) })
    }
  }, [endedKey, profile, queryClient])

  const cancel = useMutation({
    mutationFn: (jobId: string) => api.cancelJob(jobId),
    onSuccess: (job) => queryClient.setQueryData(queryKeys.job(job.id), job),
    onError: (error, jobId) => {
      if (error instanceof ApiError && error.code === 'job_not_running') {
        // It just finished: the next read shows how, and the row goes away.
        void queryClient.invalidateQueries({ queryKey: queryKeys.job(jobId) })
        return
      }
      setCancelling((prev) => {
        const next = new Set(prev)
        next.delete(jobId)
        return next
      })
      if (error instanceof ApiError && error.status === 401) return
      toast.error({
        title: 'Could not cancel the pull',
        description: error instanceof ApiError && error.status !== 0 ? error.message : 'Could not reach the server.',
        key: `pull-cancel:${jobId}`,
      })
    },
  })

  return {
    running,
    /** Follows a pull the server just accepted. */
    track: (jobId: string, reference: string) => {
      const pull = { profile, jobId, reference }
      rememberPull(pull)
      setAll((prev) => [...prev.filter((p) => p.jobId !== jobId), pull])
    },
    /** Asks the server to cancel once; the row then reads "Cancelling…" until the job ends. */
    cancel: (jobId: string) => {
      if (cancelling.has(jobId)) return
      setCancelling((prev) => new Set(prev).add(jobId))
      cancel.mutate(jobId)
    },
  }
}
