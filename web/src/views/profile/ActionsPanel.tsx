import { useActions } from '@/lib/api/useActions'

const note = 'rounded-lg border border-border px-3 py-3 text-sm text-muted-foreground'

const OUTCOME_LABEL: Record<string, string> = {
  success: 'success',
  failed: 'failed',
  cancelled: 'cancelled',
}

function formatTime(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toISOString().replace('T', ' ').slice(0, 19) + ' UTC'
}

/** Recent action log entries. Any error shows an inline "not available" state. */
export function ActionsPanel({ profile }: { profile: string }) {
  const query = useActions(profile, 20)
  const entries = query.data?.entries

  let body
  if (query.isPending) {
    body = <p className={note}>Loading actions…</p>
  } else if (!entries) {
    body = <p className={note}>Action history is not available.</p>
  } else if (entries.length === 0) {
    body = <p className={note}>No actions recorded yet.</p>
  } else {
    body = (
      <ul className="flex flex-col divide-y divide-border rounded-lg border border-border text-sm">
        {entries.map((e, i) => (
          <li key={`${e.startedAt}-${i}`} className="flex flex-wrap items-baseline gap-x-3 px-3 py-2">
            <span className="font-mono">{e.action}</span>
            <span
              className={e.outcome === 'failed' ? 'text-destructive' : 'text-muted-foreground'}
            >
              {OUTCOME_LABEL[e.outcome] ?? e.outcome}
            </span>
            <time dateTime={e.startedAt} className="font-mono text-xs text-muted-foreground">
              {formatTime(e.startedAt)}
            </time>
            {e.detail && <span className="text-muted-foreground">{e.detail}</span>}
          </li>
        ))}
      </ul>
    )
  }

  return (
    <section aria-labelledby="actions-heading" className="flex flex-col gap-2">
      <h3 id="actions-heading" className="text-base font-semibold">
        Recent actions
      </h3>
      {body}
    </section>
  )
}
