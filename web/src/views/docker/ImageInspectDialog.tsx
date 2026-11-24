import { useQuery } from '@tanstack/react-query'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { ApiError } from '@/lib/api/client'
import { api } from '@/lib/api/endpoints'
import { queryKeys } from '@/lib/api/keys'
import { formatBytes } from '@/lib/format'
import { plural } from './volumeText'

function errorText(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.status === 0) return 'Could not reach the server.'
    if (error.code === 'docker_unavailable') return 'Docker is not reachable in this VM yet.'
    if (error.status === 404) return 'This image no longer exists. Close this panel and refresh the list.'
  }
  return 'The image could not be loaded.'
}

function when(iso: string | null): string {
  if (iso === null) return 'unknown time'
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString()
}

/**
 * Read-only details of one image. They are requested once when the panel opens
 * and only again on Refresh, never on a timer. The server sends no build
 * commands, environment or labels, so there is nothing of the kind to show.
 */
export function ImageInspectDialog({
  profile,
  image,
  onClose,
}: {
  profile: string
  /** The 12-character id of the image to show; null keeps the panel closed. */
  image: string | null
  onClose: () => void
}) {
  const open = image !== null
  const query = useQuery({
    queryKey: queryKeys.image(profile, image ?? ''),
    queryFn: ({ signal }) => api.getImage(profile, image ?? '', signal),
    enabled: open,
    retry: false,
    staleTime: 0,
    gcTime: 0,
    refetchInterval: false,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
  })
  const d = query.data

  let body
  if (query.isPending) {
    body = <p className="text-sm text-muted-foreground">Loading image…</p>
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
    const hidden = Math.max(0, d.layerCount - d.layers.length)
    body = (
      <div className="flex flex-col gap-3 text-sm">
        <dl className="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1">
          <dt className="text-muted-foreground">ID</dt>
          <dd className="font-mono break-all">{d.id}</dd>
          <dt className="text-muted-foreground">Size</dt>
          <dd className="font-mono">{formatBytes(d.sizeBytes)}</dd>
          <dt className="text-muted-foreground">Created</dt>
          <dd>{d.createdAt === null ? 'Unknown' : when(d.createdAt)}</dd>
          <dt className="text-muted-foreground">Architecture</dt>
          <dd>{d.architecture || 'Unknown'}</dd>
          <dt className="text-muted-foreground">OS</dt>
          <dd>{d.os || 'Unknown'}</dd>
          <dt className="text-muted-foreground">Exposed ports</dt>
          <dd className="font-mono">{d.exposedPorts.length === 0 ? <span className="font-sans">None</span> : d.exposedPorts.join(', ')}</dd>
        </dl>
        <section aria-labelledby="inspect-tags">
          <h3 id="inspect-tags" className="mb-1 font-medium">
            Tags
          </h3>
          {d.tags.length === 0 ? (
            <p className="text-muted-foreground">No tags.</p>
          ) : (
            <ul className="list-inside list-disc font-mono text-xs">
              {d.tags.map((t) => (
                <li key={t} className="break-all">
                  {t}
                </li>
              ))}
            </ul>
          )}
        </section>
        <section aria-labelledby="inspect-digests">
          <h3 id="inspect-digests" className="mb-1 font-medium">
            Digests
          </h3>
          {d.digests.length === 0 ? (
            <p className="text-muted-foreground">No digests.</p>
          ) : (
            <ul className="list-inside list-disc font-mono text-xs">
              {d.digests.map((t) => (
                <li key={t} className="break-all">
                  {t}
                </li>
              ))}
            </ul>
          )}
        </section>
        <section aria-labelledby="inspect-containers">
          <h3 id="inspect-containers" className="mb-1 font-medium">
            Containers using this image
          </h3>
          {d.containers === 0 ? (
            <p className="text-muted-foreground">No container uses this image.</p>
          ) : (
            <>
              <p className="text-muted-foreground">{plural(d.containers, 'container', 'containers')}, running or stopped.</p>
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
        <section aria-labelledby="inspect-layers">
          <h3 id="inspect-layers" className="mb-1 font-medium">
            Layers
          </h3>
          {d.layerCount === 0 && d.layers.length === 0 ? (
            <p className="text-muted-foreground">No layer information.</p>
          ) : (
            <>
              <p className="text-muted-foreground">
                {plural(d.layerCount, 'layer', 'layers')}, newest first. Only the time and size of each layer are shown.
              </p>
              <ol aria-label="Layers" className="max-h-48 list-inside list-decimal overflow-y-auto font-mono text-xs">
                {d.layers.map((l, i) => (
                  <li key={i}>
                    {when(l.createdAt)}, {formatBytes(l.sizeBytes)}
                  </li>
                ))}
              </ol>
              {hidden > 0 && <p className="mt-1 text-muted-foreground">and {plural(hidden, 'more layer', 'more layers')} not listed</p>}
            </>
          )}
        </section>
      </div>
    )
  }

  return (
    <Dialog open={open} onOpenChange={(next) => !next && onClose()}>
      <DialogContent className="top-[8%] max-h-[85vh] max-w-lg overflow-y-auto">
        <DialogTitle>Image {image}</DialogTitle>
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
