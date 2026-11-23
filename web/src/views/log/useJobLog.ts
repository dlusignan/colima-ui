import { useMemo } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ApiError } from '@/lib/api/client'
import { api } from '@/lib/api/endpoints'
import { queryKeys } from '@/lib/api/keys'
import type { Job, JobState } from '@/lib/api/types'
import { useJobOutput } from '@/lib/events'
import { mergeLines } from './mergeLines'

/**
 * Follows one job: the captured output from REST merged with streamed lines.
 * The events hooks subscribe first (buffering by seq), the REST job fills in
 * everything earlier, and a reconnect refetches the job (the bridge
 * invalidates the job queries on resync), so no line is lost or shown twice.
 */
export function useJobLog(jobId: string | undefined) {
  const queryClient = useQueryClient()
  const live = useJobOutput(jobId)
  const query = useQuery({
    queryKey: queryKeys.job(jobId ?? ''),
    queryFn: ({ signal }) => api.getJob(jobId as string, signal),
    enabled: !!jobId,
    retry: (count, error) => !(error instanceof ApiError) && count < 2,
  })
  const job = query.data

  const lines = useMemo(
    () => mergeLines(job?.output ?? [], live.lines),
    [job?.output, live.lines],
  )

  // The finished event can arrive before the refetch lands.
  const state: JobState | undefined =
    live.finished?.state ?? job?.state
  const exitCode = live.finished ? live.finished.exitCode : (job?.exitCode ?? null)

  const cancel = useMutation({
    mutationFn: () => api.cancelJob(jobId as string),
    onSuccess: (updated: Job) => {
      queryClient.setQueryData(queryKeys.job(updated.id), updated)
      void queryClient.invalidateQueries({ queryKey: queryKeys.profiles })
    },
    onError: (error) => {
      // 409 job_not_running: the job just finished, refetch to show it.
      if (error instanceof ApiError && error.code === 'job_not_running') {
        void queryClient.invalidateQueries({ queryKey: queryKeys.job(jobId as string) })
      }
    },
  })

  return {
    job,
    lines,
    state,
    exitCode,
    loading: query.isPending && !!jobId,
    notFound: query.error instanceof ApiError && query.error.code === 'job_not_found',
    failed: query.isError && !(query.error instanceof ApiError && query.error.code === 'job_not_found'),
    cancel,
  }
}
