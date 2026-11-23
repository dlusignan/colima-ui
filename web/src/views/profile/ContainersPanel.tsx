import { Link } from 'react-router-dom'
import { ApiError } from '@/lib/api/client'
import { useContainers } from '@/lib/api/useContainers'
import { formatBytes } from '@/lib/format'
import type { Profile } from '@/lib/api/types'

const note = 'rounded-lg border border-border px-3 py-3 text-sm text-muted-foreground'

function errorText(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.code === 'docker_unavailable') return 'Docker is not reachable in this VM yet.'
    if (error.status === 0) return 'Could not reach the server.'
  }
  return 'Containers could not be loaded.'
}

/** Containers of a running VM. Nothing is requested while the VM is not running. */
export function ContainersPanel({ profile }: { profile: Profile }) {
  const running = profile.status === 'running'
  const query = useContainers(profile.name, running)
  const list = query.data?.containers

  let body
  if (!running) {
    body = <p className={note}>Start the profile to see its containers.</p>
  } else if (query.isPending) {
    body = <p className={note}>Loading containers…</p>
  } else if (query.isError && !list) {
    body = (
      <p role="alert" className="rounded-lg border border-destructive px-3 py-2 text-sm">
        {errorText(query.error)}
      </p>
    )
  } else if (!list || list.length === 0) {
    body = (
      <p className={note}>
        {profile.runtime === 'docker'
          ? 'No containers.'
          : 'Container data is available for the docker runtime only.'}
      </p>
    )
  } else {
    body = (
      <div className="overflow-x-auto rounded-lg border border-border" tabIndex={0} role="region" aria-label="Containers table">
        <table className="w-full text-sm">
          <caption className="sr-only">Containers in {profile.name}</caption>
          <thead>
            <tr className="border-b border-border text-left text-xs text-muted-foreground">
              {['Name', 'Image', 'State', 'CPU', 'Memory'].map((c) => (
                <th key={c} scope="col" className="px-3 py-2 font-medium">
                  {c}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {list.map((c) => (
              <tr key={c.id} className="border-b border-border last:border-0">
                <th scope="row" className="px-3 py-2 text-left font-mono font-medium">
                  {c.name}
                </th>
                <td className="px-3 py-2 font-mono">{c.image}</td>
                <td className="px-3 py-2">{c.state}</td>
                <td className="px-3 py-2 font-mono">{c.cpuPercent.toFixed(1)}%</td>
                <td className="px-3 py-2 font-mono">{formatBytes(c.memoryBytes)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    )
  }

  return (
    <section aria-labelledby="containers-heading" className="flex flex-col gap-2">
      <div className="flex items-baseline justify-between">
        <h3 id="containers-heading" className="text-base font-semibold">
          Containers
        </h3>
        <Link
          className="text-sm underline underline-offset-4"
          to={`/profiles/${encodeURIComponent(profile.name)}/capacity`}
        >
          Capacity
        </Link>
      </div>
      {body}
    </section>
  )
}
