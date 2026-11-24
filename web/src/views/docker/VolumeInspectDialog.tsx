import { useQuery } from '@tanstack/react-query'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { ApiError } from '@/lib/api/client'
import { api } from '@/lib/api/endpoints'
import { queryKeys } from '@/lib/api/keys'
import { formatBytes } from '@/lib/format'

function errorText(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.status === 0) return 'Could not reach the server.'
    if (error.code === 'docker_unavailable') return 'Docker is not reachable in this VM yet.'
    if (error.status === 404) return 'This volume no longer exists. Close this panel and refresh the list.'
  }
  return 'The volume could not be loaded.'
}

/**
 * Read-only details of one volume. The details are requested once when the
 * panel opens (the size can be slow to compute) and only again on Refresh,
 * never on a timer. No host path is shown. Label values are plain text.
 */
export function VolumeInspectDialog({
  profile,
  volume,
  onClose,
}: {
  profile: string
  /** Name of the volume to show; null keeps the panel closed. */
  volume: string | null
  onClose: () => void
}) {
  const open = volume !== null
  const query = useQuery({
    queryKey: queryKeys.volume(profile, volume ?? ''),
    queryFn: ({ signal }) => api.getVolume(profile, volume ?? '', signal),
    enabled: open,
    retry: false,
    staleTime: 0,
    gcTime: 0,
    refetchInterval: false,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
  })
  const d = query.data
  const created = d ? new Date(d.createdAt) : null

  let body
  if (query.isPending) {
    body = <p className="text-sm text-muted-foreground">Loading volume…</p>
  } else if (query.isError || !d) {
    body = (
      <div role="alert" className="flex flex-col items-start gap-2 rounded-lg border border-destructive px-3 py-2 text-sm">
        <p>{errorText(query.error)}</p>
        {!(query.error instanceof ApiError && query.error.status === 404) && (
          <Button type="button" variant="outline" size="sm" onClick={() => void query.refetch()}>
            Retry
          </Button>
        )}
      </div>
    )
  } else {
    const labels = Object.entries(d.labels).sort(([a], [b]) => a.localeCompare(b))
    body = (
      <div className="flex flex-col gap-3 text-sm">
        <dl className="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1">
          <dt className="text-muted-foreground">Name</dt>
          <dd className="font-mono break-all">{d.name}</dd>
          <dt className="text-muted-foreground">Driver</dt>
          <dd>{d.driver}</dd>
          <dt className="text-muted-foreground">Created</dt>
          <dd>{created && !Number.isNaN(created.getTime()) ? created.toLocaleString() : d.createdAt}</dd>
          <dt className="text-muted-foreground">Size</dt>
          <dd className="font-mono">{d.sizeBytes === null ? 'Unknown' : formatBytes(d.sizeBytes)}</dd>
        </dl>
        <section aria-labelledby="inspect-labels">
          <h3 id="inspect-labels" className="mb-1 font-medium">
            Labels
          </h3>
          {labels.length === 0 ? (
            <p className="text-muted-foreground">No labels.</p>
          ) : (
            <dl className="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1 font-mono text-xs">
              {labels.map(([k, v]) => (
                <div key={k} className="contents">
                  <dt className="break-all">{k}</dt>
                  <dd className="break-all">{v === '' ? <span className="text-muted-foreground">(empty)</span> : v}</dd>
                </div>
              ))}
            </dl>
          )}
        </section>
        <section aria-labelledby="inspect-containers">
          <h3 id="inspect-containers" className="mb-1 font-medium">
            Containers using this volume
          </h3>
          {d.containers === 0 ? (
            <p className="text-muted-foreground">No container uses this volume.</p>
          ) : (
            <>
              <p className="text-muted-foreground">
                {d.containers} {d.containers === 1 ? 'container' : 'containers'}, running or stopped.
              </p>
              <ul className="list-inside list-disc font-mono text-xs">
                {d.containerNames.map((n) => (
                  <li key={n} className="break-all">
                    {n}
                  </li>
                ))}
              </ul>
            </>
          )}
        </section>
      </div>
    )
  }

  return (
    <Dialog open={open} onOpenChange={(next) => !next && onClose()}>
      <DialogContent className="top-[8%] max-h-[85vh] max-w-lg overflow-y-auto">
        <DialogTitle>Volume {volume}</DialogTitle>
        <DialogDescription>Read-only details. They load when this panel opens and do not update by themselves.</DialogDescription>
        {body}
        <div className="flex justify-end gap-2">
          <Button type="button" variant="outline" size="sm" onClick={() => void query.refetch()} disabled={query.isFetching || query.isPending}>
            Refresh
          </Button>
          <Button type="button" size="sm" onClick={onClose}>
            Close
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  )
}
