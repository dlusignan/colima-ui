import { useQuery } from '@tanstack/react-query'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { ApiError } from '@/lib/api/client'
import { api } from '@/lib/api/endpoints'
import { queryKeys } from '@/lib/api/keys'
import { plural } from './volumeText'

function errorText(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.status === 0) return 'Could not reach the server.'
    if (error.code === 'docker_unavailable') return 'Docker is not reachable in this VM yet.'
    if (error.status === 404) return 'This network no longer exists. Close this panel and refresh the list.'
  }
  return 'The network could not be loaded.'
}

const dash = <span className="text-muted-foreground">–</span>

/**
 * Read-only details of one network. The details are requested once when the
 * panel opens and only again on Refresh, never on a timer. The request uses the
 * network's NAME (docs/api.md 3.23), not its id.
 */
export function NetworkInspectDialog({
  profile,
  network,
  onClose,
}: {
  profile: string
  /** Name of the network to show; null keeps the panel closed. */
  network: string | null
  onClose: () => void
}) {
  const open = network !== null
  const query = useQuery({
    queryKey: queryKeys.network(profile, network ?? ''),
    queryFn: ({ signal }) => api.getNetwork(profile, network ?? '', signal),
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
    body = <p className="text-sm text-muted-foreground">Loading network…</p>
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
    body = (
      <div className="flex flex-col gap-3 text-sm">
        <dl className="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1">
          <dt className="text-muted-foreground">Name</dt>
          <dd className="font-mono break-all">{d.name}</dd>
          <dt className="text-muted-foreground">Driver</dt>
          <dd>{d.driver}</dd>
          <dt className="text-muted-foreground">Scope</dt>
          <dd>{d.scope}</dd>
          <dt className="text-muted-foreground">Internal</dt>
          <dd>{d.internal ? 'Yes' : 'No'}</dd>
        </dl>
        <section aria-labelledby="inspect-subnets">
          <h3 id="inspect-subnets" className="mb-1 font-medium">
            Subnets
          </h3>
          {d.subnets.length === 0 ? (
            <p className="text-muted-foreground">No subnets.</p>
          ) : (
            <dl className="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1 font-mono text-xs">
              {d.subnets.map((s) => (
                <div key={s.subnet} className="contents">
                  <dt className="break-all">{s.subnet}</dt>
                  <dd className="break-all">{s.gateway === '' ? <span className="text-muted-foreground">no gateway</span> : `gateway ${s.gateway}`}</dd>
                </div>
              ))}
            </dl>
          )}
        </section>
        <section aria-labelledby="inspect-containers">
          <h3 id="inspect-containers" className="mb-1 font-medium">
            Attached containers
          </h3>
          {d.containers.length === 0 ? (
            <p className="text-muted-foreground">No container is attached to this network.</p>
          ) : (
            <>
              <p className="text-muted-foreground">
                {plural(d.containers.length, 'container', 'containers')}, running or stopped. Stopped containers have no address.
              </p>
              <table className="mt-1 w-full text-left font-mono text-xs">
                <caption className="sr-only">Containers attached to {d.name}</caption>
                <thead>
                  <tr className="text-muted-foreground">
                    <th scope="col" className="pr-3 font-medium">
                      Container
                    </th>
                    <th scope="col" className="pr-3 font-medium">
                      IPv4
                    </th>
                    <th scope="col" className="font-medium">
                      IPv6
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {d.containers.map((c) => (
                    <tr key={c.name}>
                      <th scope="row" className="pr-3 font-normal break-all">
                        {c.name}
                      </th>
                      <td className="pr-3 break-all">{c.ipv4Address || dash}</td>
                      <td className="break-all">{c.ipv6Address || dash}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </>
          )}
        </section>
      </div>
    )
  }

  return (
    <Dialog open={open} onOpenChange={(next) => !next && onClose()}>
      <DialogContent className="top-[8%] max-h-[85vh] max-w-lg overflow-y-auto">
        <DialogTitle>Network {network}</DialogTitle>
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
