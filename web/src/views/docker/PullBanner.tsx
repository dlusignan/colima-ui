import { Link } from 'react-router-dom'
import { Button } from '@/components/ui/button'
import { logPath } from '@/views/log/logPath'
import type { RunningPull } from './usePullJobs'

/** One row per running pull above the images table. Renders nothing when none runs. */
export function PullBanner({
  profile,
  pulls,
  onCancel,
}: {
  profile: string
  pulls: readonly RunningPull[]
  onCancel: (jobId: string) => void
}) {
  if (pulls.length === 0) return null
  return (
    <ul aria-label="Running pulls" className="flex flex-col gap-2">
      {pulls.map((p) => (
        <li
          key={p.jobId}
          className="flex flex-wrap items-center gap-x-3 gap-y-1 rounded-lg border border-border px-3 py-2 text-sm"
        >
          <div className="flex min-w-0 flex-1 flex-col">
            <span className="break-all">
              {p.cancelling ? 'Cancelling pull of' : 'Pulling'} <span className="font-mono">{p.reference}</span>…
            </span>
            {p.lastLine && <span className="truncate font-mono text-xs text-muted-foreground">{p.lastLine}</span>}
          </div>
          <Link
            className="text-sm underline underline-offset-4"
            to={logPath(profile, p.jobId)}
            aria-label={`View log of the pull of ${p.reference}`}
          >
            View log
          </Link>
          <Button
            type="button"
            size="sm"
            variant="outline"
            aria-label={`Cancel the pull of ${p.reference}`}
            disabled={p.cancelling}
            focusableWhenDisabled
            onClick={() => onCancel(p.jobId)}
          >
            {p.cancelling ? 'Cancelling…' : 'Cancel'}
          </Button>
        </li>
      ))}
    </ul>
  )
}
