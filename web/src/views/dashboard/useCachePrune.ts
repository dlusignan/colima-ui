import { useRef, useState } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { ApiError } from '@/lib/api/client'
import { api } from '@/lib/api/endpoints'
import { queryKeys } from '@/lib/api/keys'
import { useToast } from '@/lib/toast/useToast'

const POLL_MS = 2_000
const CONFIRM = 'delete-cached-assets'

/** Fixed text for a refused or failed request. Server text is never shown. */
function refusalText(error: unknown): string | null {
  if (!(error instanceof ApiError)) return 'Could not clear the download cache.'
  if (error.status === 0) return 'Could not reach the server.'
  if (error.status === 401) return null // the auth notice takes over
  if (error.code === 'invalid_state' && error.details.reason === 'assets_in_use') {
    return 'A profile is being created or started and may be downloading these files. Try again when it is done.'
  }
  if (error.code === 'conflict') return 'The download cache is already being cleared.'
  if (error.code === 'shutting_down') return 'The server is shutting down.'
  return 'Could not clear the download cache.'
}

/**
 * Runs and follows the one global cache-prune job. It polls
 * `GET /api/jobs/{id}` every 2 s while the job runs (the same reasoning as
 * usePullJobs: no event stream needed, a missed event cannot leave it stuck)
 * and refreshes nothing when it ends: the profile, image and engine data do not
 * change. The outcome toast belongs to JobOutcomeToasts. A reload forgets the
 * job; starting another then answers `conflict` while the first still runs.
 */
export function useCachePrune() {
  const toast = useToast()
  const [jobId, setJobId] = useState<string | null>(null)
  const [cancelling, setCancelling] = useState(false)
  // Set at the first send until the request settles, so a double click or a
  // second dialog confirm in the same tick cannot send two requests.
  const sent = useRef(false)

  const start = useMutation({
    mutationFn: (includeLima: boolean) => api.pruneCache({ confirm: CONFIRM, includeLima }),
    onSuccess: (accepted) => {
      setCancelling(false)
      setJobId(accepted.jobId)
    },
    onSettled: () => {
      // From here `running` covers a second click (the job id is set).
      sent.current = false
    },
    onError: (error) => {
      const text = refusalText(error)
      if (text) toast.error({ title: text, key: 'cache-prune-error' })
    },
  })

  const job = useQuery({
    queryKey: jobId ? queryKeys.job(jobId) : ['jobs', 'none'],
    queryFn: ({ signal }) => api.getJob(jobId ?? '', signal),
    enabled: jobId !== null,
    refetchInterval: (query) => (query.state.data?.state === 'running' ? POLL_MS : false),
    retry: (count, error) =>
      error instanceof ApiError ? error.code === 'job_not_found' && count < 1 : count < 2,
    retryDelay: 500,
  })

  const gone = job.error instanceof ApiError && job.error.code === 'job_not_found'
  const ended = jobId !== null && (gone || (job.data !== undefined && job.data.state !== 'running'))

  const cancel = useMutation({
    mutationFn: (id: string) => api.cancelJob(id),
    onSuccess: () => void job.refetch(),
    onError: (error) => {
      if (error instanceof ApiError && error.code === 'job_not_running') return // it just ended
      setCancelling(false)
      if (error instanceof ApiError && error.status === 401) return
      toast.error({ title: 'Could not cancel clearing the cache.', key: 'cache-prune-cancel' })
    },
  })

  const lines = job.data?.output ?? []
  return {
    running: start.isPending || (jobId !== null && !ended),
    cancelling,
    lastLine: lines.length > 0 ? lines[lines.length - 1].line : null,
    /** Sends the request once; later calls do nothing while it is in flight or the job runs. */
    run: (includeLima: boolean) => {
      if (sent.current || start.isPending || (jobId !== null && !ended)) return
      sent.current = true
      start.mutate(includeLima)
    },
    /** Asks the server to cancel once; the card reads "Cancelling…" until the job ends. */
    cancel: () => {
      if (!jobId || cancelling) return
      setCancelling(true)
      cancel.mutate(jobId)
    },
  }
}
