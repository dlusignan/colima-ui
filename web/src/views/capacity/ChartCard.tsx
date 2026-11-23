import { useId, useState, useSyncExternalStore } from 'react'
import { UPlotChart } from './UPlotChart'
import { thinRows } from './thinRows'
import type { SeriesBuffer, SeriesKey } from './SeriesBuffer'

export interface ChartCardProps {
  title: string
  help: string
  buffer: SeriesBuffer
  seriesKey: SeriesKey
  colorIndex: 0 | 1 | 2
  limit: number
  formatValue: (value: number) => string
}

const timeFmt = new Intl.DateTimeFormat('en-US', {
  hour: '2-digit',
  minute: '2-digit',
  second: '2-digit',
})

function describe(
  title: string,
  used: number | null | undefined,
  limit: number,
  format: (v: number) => string,
): string {
  if (used === null || used === undefined) {
    return `${title}: no reading yet. Allocated ${format(limit)}.`
  }
  const pct = limit > 0 ? Math.round((used / limit) * 100) : 0
  return `${title}: ${format(used)} used of ${format(limit)} allocated (${pct}%).`
}

/** One chart with a text alternative: an aria-label and a "Show data" table. */
export function ChartCard({
  title,
  help,
  buffer,
  seriesKey,
  colorIndex,
  limit,
  formatValue,
}: ChartCardProps) {
  useSyncExternalStore(buffer.subscribe, () => buffer.version)
  const [open, setOpen] = useState(false)
  const tableId = useId()
  const latest = buffer.latest()
  const label = describe(title, latest?.[seriesKey], limit, formatValue)
  const rows = open ? thinRows(buffer.snapshot()) : []

  return (
    <section className="flex flex-col gap-2 rounded-lg border border-border bg-card p-3">
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <h3 className="text-sm font-semibold">{title}</h3>
        <p className="flex items-center gap-3 text-xs text-muted-foreground">
          <span>Used (solid)</span>
          <span>Allocated (dashed): {formatValue(limit)}</span>
        </p>
      </div>
      <p className="text-xs text-muted-foreground">{help}</p>
      <figure role="img" aria-label={label} className="m-0">
        <UPlotChart
          buffer={buffer}
          seriesKey={seriesKey}
          colorIndex={colorIndex}
          limit={limit}
          formatValue={formatValue}
        />
      </figure>
      <div>
        <button
          type="button"
          className="text-xs underline underline-offset-2"
          aria-expanded={open}
          aria-controls={tableId}
          onClick={() => setOpen((o) => !o)}
        >
          {open ? 'Hide data' : 'Show data'}
        </button>
        <div id={tableId} hidden={!open}>
          {open && (
            <table className="mt-2 w-full text-left font-mono text-xs">
              <caption className="sr-only">{title} samples</caption>
              <thead>
                <tr>
                  <th scope="col">Time</th>
                  <th scope="col">Used</th>
                  <th scope="col">Allocated</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((p) => {
                  const v = p[seriesKey]
                  return (
                    <tr key={p.t}>
                      <td>{timeFmt.format(new Date(p.t * 1000))}</td>
                      <td>{v === null ? 'no reading' : formatValue(v)}</td>
                      <td>{formatValue(limit)}</td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          )}
        </div>
      </div>
    </section>
  )
}
