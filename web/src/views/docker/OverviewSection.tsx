import { useQueryClient } from '@tanstack/react-query'
import { ApiError } from '@/lib/api/client'
import { queryKeys } from '@/lib/api/keys'
import { useEngine } from '@/lib/api/useEngine'
import { formatBytes, formatCount } from '@/lib/format'
import type { EngineInfo, Profile } from '@/lib/api/types'
import { diskSegments } from './diskUsage'
import { SectionError, noteClass } from './SectionState'

const swatch: Record<string, string> = {
  images: 'bg-chart-1',
  containers: 'bg-chart-2',
  volumes: 'bg-chart-3',
  buildCache: 'bg-chart-4',
}

function containerSummary(c: EngineInfo['containers']): string {
  if (c.total === 0) return 'None'
  const parts = [`${c.running} running`]
  if (c.paused > 0) parts.push(`${c.paused} paused`)
  parts.push(`${c.stopped} stopped`)
  return parts.join(', ')
}

function isNotDocker(error: unknown): boolean {
  return error instanceof ApiError && error.status === 409 && error.details.runtime === 'not_docker'
}

function DiskUsage({ usage }: { usage: EngineInfo['diskUsage'] }) {
  const { segments, total } = diskSegments(usage)
  const figures = segments.map((s) => `${s.label} ${formatBytes(s.bytes)}`).join(', ')
  return (
    <div className="flex flex-col gap-2">
      <h4 className="text-sm font-medium">Disk usage</h4>
      <div className="flex items-center gap-3">
        <div
          role="img"
          aria-label={`Docker disk usage: ${figures}. Total ${formatBytes(total)}.`}
          className="flex h-3 flex-1 overflow-hidden rounded-full border border-border bg-muted"
        >
          {segments.map((s) =>
            s.percent > 0 ? (
              <div key={s.key} className={swatch[s.key]} style={{ width: `${s.percent}%` }} />
            ) : null,
          )}
        </div>
        <span className="font-mono text-sm whitespace-nowrap">Total {formatBytes(total)}</span>
      </div>
      <ul className="flex flex-wrap gap-x-4 gap-y-1 text-sm">
        {segments.map((s) => (
          <li key={s.key} className="flex items-center gap-1.5">
            <span aria-hidden="true" className={`inline-block size-2.5 rounded-sm ${swatch[s.key]}`} />
            <span>{s.label}</span>
            <span className="font-mono">{formatBytes(s.bytes)}</span>
          </li>
        ))}
      </ul>
    </div>
  )
}

/** Engine facts and Docker disk usage of a running VM. Nothing is requested while the VM is not running. */
export function OverviewSection({ profile }: { profile: Profile }) {
  const running = profile.status === 'running'
  const query = useEngine(profile.name, running)
  const queryClient = useQueryClient()
  const info = query.data

  let body
  if (!running) {
    body = <p className={noteClass}>Start the profile to see its Docker overview.</p>
  } else if (query.isPending) {
    body = <p className={noteClass}>Loading overview…</p>
  } else if (isNotDocker(query.error)) {
    body = <p className={noteClass}>This profile does not use Docker.</p>
  } else if (query.isError && !info) {
    body = (
      <SectionError
        error={query.error}
        fallback="The overview could not be loaded."
        onRetry={() => void queryClient.invalidateQueries({ queryKey: queryKeys.engine(profile.name) })}
      />
    )
  } else if (info) {
    const facts: Array<[string, string]> = [
      ['Docker version', info.version],
      ['API version', info.apiVersion],
      ['OS / architecture', `${info.os} / ${info.arch}`],
      ['Containers', containerSummary(info.containers)],
      ['Images', formatCount(info.images)],
      ['Engine CPUs', formatCount(info.cpus)],
      ['Engine memory', formatBytes(info.memoryBytes)],
    ]
    body = (
      <div className="flex flex-col gap-4 rounded-lg border border-border px-3 py-3">
        <dl className="grid grid-cols-1 gap-x-6 gap-y-2 text-sm sm:grid-cols-2 lg:grid-cols-4">
          {facts.map(([label, value]) => (
            <div key={label}>
              <dt className="text-xs text-muted-foreground">{label}</dt>
              <dd className="font-mono">{value}</dd>
            </div>
          ))}
        </dl>
        <DiskUsage usage={info.diskUsage} />
      </div>
    )
  }

  return (
    <section aria-labelledby="overview-heading" className="flex flex-col gap-2">
      <h3 id="overview-heading" className="text-base font-semibold">
        Overview
      </h3>
      {body}
    </section>
  )
}
