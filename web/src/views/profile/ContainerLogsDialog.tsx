import { useLayoutEffect, useRef, useState } from 'react'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { ApiError } from '@/lib/api/client'
import { useContainerLogs } from '@/lib/api/useContainerLogs'

const TAILS = [100, 200, 500, 1000]

function errorText(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.status === 0) return 'Could not reach the server.'
    if (error.code === 'docker_unavailable') return 'Docker is not reachable in this VM yet.'
    if (error.code === 'invalid_state' && error.details.reason === 'logs_unavailable') {
      return "This container's log driver does not keep logs readable here."
    }
    if (error.code === 'container_not_found') {
      return 'This container no longer exists. Close this dialog and refresh the container list.'
    }
  }
  return 'The logs could not be loaded.'
}

function isRetryable(error: unknown): boolean {
  if (!(error instanceof ApiError)) return true
  return !(error.code === 'container_not_found' || (error.code === 'invalid_state' && error.details.reason === 'logs_unavailable'))
}

/** A Logs button for one container and the dialog it opens: a read-only, plain text snapshot. */
export function ContainerLogsDialog({
  profile,
  container,
}: {
  profile: string
  container: { id: string; name: string }
}) {
  const [open, setOpen] = useState(false)
  const [tail, setTail] = useState(200)
  const [copy, setCopy] = useState<'idle' | 'done' | 'failed'>('idle')
  const query = useContainerLogs(profile, container.id, tail, open)
  const region = useRef<HTMLPreElement>(null)
  const lines = query.data?.lines
  const text = lines ? lines.join('\n') : ''

  useLayoutEffect(() => {
    const el = region.current
    if (el) el.scrollTop = el.scrollHeight
  }, [lines, open])

  async function onCopy() {
    try {
      if (!navigator.clipboard) throw new Error('unavailable')
      await navigator.clipboard.writeText(text)
      setCopy('done')
    } catch {
      setCopy('failed')
    }
  }

  let body
  if (query.isPending) {
    body = <p className="text-sm text-muted-foreground">Loading logs…</p>
  } else if (query.isError) {
    body = (
      <div role="alert" className="flex flex-col items-start gap-2 rounded-lg border border-destructive px-3 py-2 text-sm">
        <p>{errorText(query.error)}</p>
        {isRetryable(query.error) && (
          <Button type="button" variant="outline" size="sm" onClick={() => void query.refetch()}>
            Retry
          </Button>
        )}
      </div>
    )
  } else if (lines && lines.length === 0) {
    body = <p className="text-sm text-muted-foreground">This container has produced no output.</p>
  } else {
    body = (
      <>
        {query.data?.truncated && <p className="text-sm text-muted-foreground">Some output is not shown.</p>}
        <pre
          ref={region}
          role="log"
          tabIndex={0}
          aria-label={`Logs of ${container.name}`}
          className="max-h-[50vh] overflow-auto rounded-lg border border-border bg-background p-2 font-mono text-xs break-words whitespace-pre-wrap"
        >
          {text}
        </pre>
      </>
    )
  }

  return (
    <>
      <Button
        type="button"
        variant="outline"
        size="sm"
        aria-label={`Logs for ${container.name}`}
        onClick={() => {
          setCopy('idle')
          setOpen(true)
        }}
      >
        Logs
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="top-[8%] max-w-3xl">
          <DialogTitle>Logs for {container.name}</DialogTitle>
          <DialogDescription>A snapshot of recent output, oldest first. It does not update by itself.</DialogDescription>
          <div className="flex flex-wrap items-center gap-2">
            <label className="flex items-center gap-2 text-sm">
              Lines
              <select
                className="rounded-md border border-border bg-background px-2 py-1 text-sm"
                value={tail}
                onChange={(e) => {
                  setTail(Number(e.target.value))
                  setCopy('idle')
                }}
              >
                {TAILS.map((n) => (
                  <option key={n} value={n}>
                    {n}
                  </option>
                ))}
              </select>
            </label>
            <Button type="button" variant="outline" size="sm" onClick={() => void query.refetch()} disabled={query.isFetching}>
              Refresh
            </Button>
            <Button type="button" variant="outline" size="sm" onClick={() => void onCopy()} disabled={!text}>
              Copy
            </Button>
            <span role="status" className="text-sm text-muted-foreground">
              {copy === 'done' && 'Copied.'}
              {copy === 'failed' && 'Could not copy to the clipboard.'}
            </span>
            <Button type="button" size="sm" className="ml-auto" onClick={() => setOpen(false)}>
              Close
            </Button>
          </div>
          {body}
        </DialogContent>
      </Dialog>
    </>
  )
}
