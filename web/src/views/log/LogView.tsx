import { useParams, useSearchParams, Link } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { Button } from '@/components/ui/button'
import { api } from '@/lib/api/endpoints'
import { queryKeys } from '@/lib/api/keys'
import type { JobState } from '@/lib/api/types'
import { TerminalPane } from './TerminalPane'
import { useJobLog } from './useJobLog'

const BADGE: Record<JobState, string> = {
  running: 'bg-warning/15 text-warning',
  succeeded: 'bg-success/15 text-success',
  failed: 'bg-destructive/15 text-destructive',
  cancelled: 'bg-muted text-muted-foreground',
}

export function LogView() {
  const { name = '' } = useParams()
  const [params] = useSearchParams()
  const fromUrl = params.get('job')
  const profile = useQuery({
    queryKey: queryKeys.profile(name),
    queryFn: ({ signal }) => api.getProfile(name, signal),
    enabled: !fromUrl && !!name,
  })
  const jobId = fromUrl ?? profile.data?.activeJobId ?? undefined
  const log = useJobLog(jobId)

  if (!jobId) {
    return (
      <p className="text-sm text-muted-foreground">
        {profile.isPending && !fromUrl && !!name
          ? 'Loading...'
          : 'No operation to show. Start, stop or restart the profile to see its output here.'}
      </p>
    )
  }
  if (log.notFound) {
    return (
      <div className="flex flex-col gap-2 text-sm">
        <p className="text-muted-foreground">
          This job is no longer available. Job output is kept in memory only and is lost when
          the backend restarts.
        </p>
        <Link
          className="text-primary underline-offset-4 hover:underline"
          to={`/profiles/${encodeURIComponent(name)}`}
        >
          Back to the profile
        </Link>
      </div>
    )
  }
  if (log.failed) {
    return (
      <p role="alert" className="text-sm text-destructive">
        Could not load the job output.
      </p>
    )
  }
  if (log.loading || !log.job) {
    return <p className="text-sm text-muted-foreground">Loading...</p>
  }

  const { job } = log
  const state = log.state ?? job.state
  return (
    <section className="flex flex-col gap-3" aria-label="Operation log">
      <div className="flex flex-wrap items-center gap-3 text-sm">
        <span className="font-mono">{job.action}</span>
        <span
          data-testid="job-state"
          className={`rounded-md px-2 py-0.5 font-mono text-xs ${BADGE[state]}`}
        >
          {state}
        </span>
        {state !== 'running' && log.exitCode !== null && (
          <span className="font-mono text-xs text-muted-foreground">exit {log.exitCode}</span>
        )}
        {state === 'running' && (
          <Button
            size="sm"
            variant="destructive"
            disabled={log.cancel.isPending}
            onClick={() => log.cancel.mutate()}
          >
            Cancel
          </Button>
        )}
      </div>
      <TerminalPane
        lines={log.lines}
        notice={
          job.truncated ? `${job.droppedLines} earlier lines were dropped` : undefined
        }
      />
    </section>
  )
}
