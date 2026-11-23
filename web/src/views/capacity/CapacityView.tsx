import { useEffect, useMemo, useState, useSyncExternalStore } from 'react'
import { useParams } from 'react-router-dom'
import { useContainers } from '@/lib/api/useContainers'
import { MAX_POINTS, RANGES, DEFAULT_RANGE_ID, rangeById, useMetricHistory } from '@/lib/api/useMetricHistory'
import { useProfiles } from '@/lib/api/useProfiles'
import { useEventListener } from '@/lib/events'
import { formatBytes, formatPercent } from '@/lib/format'
import { ChartCard } from './ChartCard'
import { ContainerTable } from './ContainerTable'
import { SeriesBuffer } from './SeriesBuffer'

const cpuFormat = (v: number) => formatPercent(v)

/** Allocated versus used for one profile, plus the per-container breakdown. */
export function CapacityView() {
  const { name = '' } = useParams()
  const [rangeId, setRangeId] = useState(DEFAULT_RANGE_ID)
  const range = rangeById(rangeId)
  const profiles = useProfiles()
  const profile = profiles.data?.profiles.find((p) => p.name === name)
  const history = useMetricHistory(name, range.id)

  // A new buffer per profile and range; `name` only forces the reset.
  const buffer = useMemo(
    () => new SeriesBuffer(range.ms, range.ms / MAX_POINTS),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [name, range.ms],
  )
  useEffect(() => {
    if (history.data) buffer.setHistory(history.data.points)
  }, [history.data, buffer])
  useEventListener('metric', (e) => buffer.append(e), { profile: name, scope: 'vm' })
  useSyncExternalStore(buffer.subscribe, () => buffer.version)

  const running = profile?.status === 'running'
  const containers = useContainers(name, running)

  let body
  if (profiles.isPending || history.isPending) {
    body = <p className="text-sm text-muted-foreground">Loading...</p>
  } else if (!profile) {
    body = (
      <p role="alert" className="text-sm text-destructive">
        Profile {name} was not found.
      </p>
    )
  } else if (history.isError && buffer.length === 0) {
    body = (
      <p role="alert" className="text-sm text-destructive">
        Could not load the metric history.
      </p>
    )
  } else if (buffer.length === 0) {
    body = (
      <p role="status" className="text-sm text-muted-foreground">
        No samples in this range yet. Samples are recorded while the VM runs a Docker
        runtime.
      </p>
    )
  } else {
    body = (
      <div className="flex flex-col gap-3">
        <ChartCard
          title="CPU"
          help="Sum of container CPU, where 100% is one full core. The limit is the CPU count times 100%."
          buffer={buffer}
          seriesKey="cpu"
          colorIndex={0}
          limit={profile.cpus * 100}
          formatValue={cpuFormat}
        />
        <ChartCard
          title="Container memory"
          help="Sum of container memory. It leaves out the guest kernel and the Docker daemon, so it sits below what the VM uses."
          buffer={buffer}
          seriesKey="mem"
          colorIndex={1}
          limit={profile.memoryBytes}
          formatValue={formatBytes}
        />
        <ChartCard
          title="Docker disk"
          help="Docker's images, containers, volumes and build cache. It leaves out other disk use in the VM."
          buffer={buffer}
          seriesKey="disk"
          colorIndex={2}
          limit={profile.diskBytes}
          formatValue={formatBytes}
        />
      </div>
    )
  }

  return (
    <section className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 className="text-xl font-semibold">Capacity: {name}</h2>
        <label className="flex items-center gap-2 text-sm">
          Time range
          <select
            className="rounded-md border border-border bg-background px-2 py-1"
            value={range.id}
            onChange={(e) => setRangeId(e.target.value)}
          >
            {RANGES.map((r) => (
              <option key={r.id} value={r.id}>
                {r.label}
              </option>
            ))}
          </select>
        </label>
      </div>
      {profile && profile.status !== 'running' && (
        <p role="status" className="text-sm text-muted-foreground">
          {profile.status === 'stopped'
            ? 'The VM is stopped. The charts show recorded history.'
            : 'The VM state is unknown. The charts show recorded history.'}
        </p>
      )}
      {body}
      {profile && (
        <section className="flex flex-col gap-2">
          <h3 className="text-sm font-semibold">Containers</h3>
          <ContainerTable
            status={
              !running
                ? 'stopped'
                : containers.isPending
                  ? 'loading'
                  : containers.isError
                    ? 'error'
                    : 'ready'
            }
            error={containers.error}
            containers={containers.data?.containers}
            cpuLimit={profile.cpus * 100}
            memoryLimit={profile.memoryBytes}
            vmLatest={buffer.latest()}
          />
        </section>
      )}
    </section>
  )
}
