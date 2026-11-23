import { Link } from 'react-router-dom'
import { ApiError } from '@/lib/api/client'
import { ContainerLogsDialog } from './ContainerLogsDialog'
import { useContainers } from '@/lib/api/useContainers'
import { containerHealth, formatPorts } from '@/lib/containerStatus'
import { formatBytes, formatRelativeTime } from '@/lib/format'
import type { Profile } from '@/lib/api/types'

const dotClass = {
  running: 'bg-success',
  paused: 'bg-muted-foreground',
  exited: 'bg-muted-foreground',
  crashed: 'bg-destructive',
  other: 'bg-muted-foreground',
}

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
              {['Name', 'Image', 'Status', 'Ports', 'Created', 'CPU', 'Memory', 'Logs'].map((c) => (
                <th key={c} scope="col" className="px-3 py-2 font-medium">
                  {c}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {list.map((c) => {
              const health = containerHealth(c.status, c.state)
              const ports = formatPorts(c.ports)
              const created = new Date(c.createdAt)
              return (
                <tr key={c.id} className="border-b border-border last:border-0 align-top">
                  <th scope="row" className="px-3 py-2 text-left font-mono font-medium">
                    {c.name}
                  </th>
                  <td className="px-3 py-2 font-mono">{c.image}</td>
                  <td className="px-3 py-2 whitespace-nowrap">
                    <span aria-hidden="true" className={`mr-2 inline-block size-2 rounded-full ${dotClass[health]}`} />
                    {c.status || c.state}
                    {health === 'paused' && <span className="ml-2 font-medium text-muted-foreground">(paused)</span>}
                    {health === 'crashed' && <span className="ml-2 font-medium text-destructive">(crashed)</span>}
                  </td>
                  <td className="px-3 py-2 font-mono whitespace-nowrap">
                    {ports.length === 0 ? (
                      <span className="font-sans">None</span>
                    ) : (
                      ports.map((p) => <div key={p}>{p}</div>)
                    )}
                  </td>
                  <td className="px-3 py-2 whitespace-nowrap">
                    <span title={Number.isNaN(created.getTime()) ? undefined : created.toLocaleString()}>
                      {formatRelativeTime(c.createdAt)}
                    </span>
                  </td>
                  <td className="px-3 py-2 font-mono">{c.cpuPercent.toFixed(1)}%</td>
                  <td className="px-3 py-2 font-mono">{formatBytes(c.memoryBytes)}</td>
                  <td className="px-3 py-2">
                    <ContainerLogsDialog profile={profile.name} container={c} />
                  </td>
                </tr>
              )
            })}
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
