import { ApiError } from '@/lib/api/client'
import type { Container } from '@/lib/api/types'
import { formatBytes, formatPercent } from '@/lib/format'
import type { SeriesPoint } from './SeriesBuffer'

export interface ContainerTableProps {
  status: 'loading' | 'stopped' | 'error' | 'ready'
  error?: unknown
  containers?: readonly Container[]
  /** Profile limits, for the proportion bars. */
  cpuLimit: number
  memoryLimit: number
  /** Latest VM sample, shown under the total. */
  vmLatest?: SeriesPoint
}

function Bar({ value, max }: { value: number; max: number }) {
  const pct = max > 0 ? Math.min(100, (value / max) * 100) : 0
  return (
    <span className="block h-1.5 w-24 rounded-sm bg-muted" aria-hidden="true">
      <span className="block h-full rounded-sm bg-primary" style={{ width: `${pct}%` }} />
    </span>
  )
}

function message(status: ContainerTableProps['status'], error: unknown): string | null {
  if (status === 'loading') return 'Loading containers...'
  if (status === 'stopped') return 'The VM is stopped, so there is no container data.'
  if (status === 'error') {
    if (error instanceof ApiError && error.code === 'invalid_state') {
      return 'The VM is stopped, so there is no container data.'
    }
    if (error instanceof ApiError && error.code === 'docker_unavailable') {
      return 'The VM is running but Docker is not answering. Container usage is unavailable.'
    }
  }
  return null
}

/** Per-container usage, sorted by memory, with the sum next to the VM figure. */
export function ContainerTable({
  status,
  error,
  containers = [],
  cpuLimit,
  memoryLimit,
  vmLatest,
}: ContainerTableProps) {
  const note = message(status, error)
  if (note) {
    return (
      <p role="status" className="text-sm text-muted-foreground">
        {note}
      </p>
    )
  }
  if (status === 'error') {
    return (
      <p role="alert" className="text-sm text-destructive">
        Could not load the containers.
      </p>
    )
  }
  if (containers.length === 0) {
    return (
      <p role="status" className="text-sm text-muted-foreground">
        No containers are running. A runtime other than Docker has no container data either.
      </p>
    )
  }
  const sorted = [...containers].sort((a, b) => b.memoryBytes - a.memoryBytes)
  const cpuSum = containers.reduce((n, c) => n + c.cpuPercent, 0)
  const memSum = containers.reduce((n, c) => n + c.memoryBytes, 0)
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-left text-sm">
        <caption className="sr-only">Usage per container</caption>
        <thead className="text-xs text-muted-foreground">
          <tr>
            <th scope="col">Container</th>
            <th scope="col">State</th>
            <th scope="col">CPU (of one core)</th>
            <th scope="col">Memory</th>
          </tr>
        </thead>
        <tbody className="font-mono">
          {sorted.map((c) => (
            <tr key={c.id}>
              <th scope="row" className="font-normal break-words">
                {c.name}
              </th>
              <td>{c.state}</td>
              <td>
                <span className="flex items-center gap-2">
                  <Bar value={c.cpuPercent} max={cpuLimit} />
                  {formatPercent(c.cpuPercent)}
                </span>
              </td>
              <td>
                <span className="flex items-center gap-2">
                  <Bar value={c.memoryBytes} max={memoryLimit} />
                  {formatBytes(c.memoryBytes)}
                </span>
              </td>
            </tr>
          ))}
        </tbody>
        <tfoot className="font-mono">
          <tr>
            <th scope="row" colSpan={2}>
              Containers total
            </th>
            <td>{formatPercent(cpuSum)}</td>
            <td>{formatBytes(memSum)}</td>
          </tr>
          <tr className="text-muted-foreground">
            <th scope="row" colSpan={2}>
              Latest VM sample
            </th>
            <td>{vmLatest?.cpu == null ? 'no reading' : formatPercent(vmLatest.cpu)}</td>
            <td>{vmLatest?.mem == null ? 'no reading' : formatBytes(vmLatest.mem)}</td>
          </tr>
        </tfoot>
      </table>
    </div>
  )
}
