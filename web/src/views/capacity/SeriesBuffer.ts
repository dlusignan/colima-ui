/** One sample on the chart's time axis; null means unknown and draws a gap. */
export interface SeriesPoint {
  /** Seconds since the epoch, the unit uPlot expects. */
  t: number
  cpu: number | null
  mem: number | null
  disk: number | null
}

export interface RawPoint {
  takenAt: string
  cpuPercent: number | null
  memoryBytes: number | null
  diskBytes: number | null
}

export type SeriesKey = 'cpu' | 'mem' | 'disk'

export function toSeriesPoint(p: RawPoint): SeriesPoint | null {
  const ms = Date.parse(p.takenAt)
  if (!Number.isFinite(ms)) return null
  return { t: ms / 1000, cpu: p.cpuPercent, mem: p.memoryBytes, disk: p.diskBytes }
}

/**
 * Time ordered points for the three charts: REST history plus live events.
 * Plain data, no React. It notifies subscribers synchronously on every change;
 * consumers throttle their own redraws.
 */
export class SeriesBuffer {
  private points: SeriesPoint[] = []
  private listeners = new Set<() => void>()
  private ver = 0

  /** `windowMs`: width of the plotted window. `minGapMs`: live points closer than this to the previous point are skipped. */
  private readonly windowMs: number
  private readonly minGapMs: number

  constructor(windowMs: number, minGapMs: number) {
    this.windowMs = windowMs
    this.minGapMs = minGapMs
  }

  get version(): number {
    return this.ver
  }

  get length(): number {
    return this.points.length
  }

  subscribe = (cb: () => void): (() => void) => {
    this.listeners.add(cb)
    return () => {
      this.listeners.delete(cb)
    }
  }

  private changed(): void {
    this.ver++
    for (const l of [...this.listeners]) l()
  }

  /** Replaces the history and keeps only live points newer than its last point. */
  setHistory(history: readonly RawPoint[]): void {
    const hist = history
      .map(toSeriesPoint)
      .filter((p): p is SeriesPoint => p !== null)
      .sort((a, b) => a.t - b.t)
    const lastT = hist.length ? hist[hist.length - 1].t : -Infinity
    this.points = [...hist, ...this.points.filter((p) => p.t > lastT)]
    this.trim()
    this.changed()
  }

  /** Adds a live event. Returns false when it was a duplicate, late or too dense. */
  append(raw: RawPoint): boolean {
    const p = toSeriesPoint(raw)
    if (!p) return false
    const last = this.points[this.points.length - 1]
    if (last) {
      if (p.t <= last.t) return false
      if ((p.t - last.t) * 1000 < this.minGapMs) return false
    }
    this.points.push(p)
    this.trim()
    this.changed()
    return true
  }

  private trim(): void {
    const last = this.points[this.points.length - 1]
    if (!last) return
    const cutoff = last.t - this.windowMs / 1000
    let i = 0
    while (i < this.points.length && this.points[i].t < cutoff) i++
    if (i > 0) this.points = this.points.slice(i)
  }

  snapshot(): readonly SeriesPoint[] {
    return this.points
  }

  latest(): SeriesPoint | undefined {
    return this.points[this.points.length - 1]
  }

  /** Aligned columns for one series: times, values (null = gap). */
  column(key: SeriesKey): { t: number[]; values: (number | null)[] } {
    return { t: this.points.map((p) => p.t), values: this.points.map((p) => p[key]) }
  }
}
