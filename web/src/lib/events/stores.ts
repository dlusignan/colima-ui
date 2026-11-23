// External stores behind the hooks. Events are applied synchronously, but
// subscribers are notified at most once per animation frame.

import type { JobFinishedEvent, JobLine, MetricEvent } from './types'

export const MAX_JOB_LINES = 10_000

class Batcher {
  private listeners = new Set<() => void>()
  private scheduled = false

  subscribe = (listener: () => void) => {
    this.listeners.add(listener)
    return () => {
      this.listeners.delete(listener)
    }
  }

  notify() {
    if (this.scheduled) return
    this.scheduled = true
    const flush = () => {
      this.scheduled = false
      this.listeners.forEach((l) => l())
    }
    if (typeof requestAnimationFrame === 'function') requestAnimationFrame(flush)
    else setTimeout(flush, 16)
  }
}

export interface JobSnapshot {
  lines: readonly JobLine[]
  lastSeq: number
  finished: JobFinishedEvent | null
}

export const EMPTY_JOB: JobSnapshot = { lines: [], lastSeq: 0, finished: null }

interface JobBuffer {
  lines: JobLine[]
  finished: JobFinishedEvent | null
  refs: number
  snapshot: JobSnapshot
}

export class JobOutputStore {
  private batcher = new Batcher()
  private jobs = new Map<string, JobBuffer>()

  subscribe = this.batcher.subscribe

  /** Starts buffering a job. Returns the release function. */
  acquire(jobId: string): () => void {
    let buf = this.jobs.get(jobId)
    if (!buf) {
      buf = { lines: [], finished: null, refs: 0, snapshot: EMPTY_JOB }
      this.jobs.set(jobId, buf)
    }
    buf.refs++
    let released = false
    return () => {
      if (released) return
      released = true
      const b = this.jobs.get(jobId)
      if (b && --b.refs <= 0) this.jobs.delete(jobId)
    }
  }

  getSnapshot(jobId: string): JobSnapshot {
    return this.jobs.get(jobId)?.snapshot ?? EMPTY_JOB
  }

  output(e: { jobId: string; seq: number; line: string }) {
    const buf = this.jobs.get(e.jobId)
    if (!buf) return
    if (this.insert(buf, { seq: e.seq, line: e.line })) this.changed(buf)
  }

  finish(e: JobFinishedEvent) {
    const buf = this.jobs.get(e.jobId)
    if (!buf) return
    buf.finished = e
    this.changed(buf)
  }

  /** Merges lines from GET /api/jobs/{id}, deduplicated by seq. */
  seed(jobId: string, lines: readonly JobLine[]) {
    const buf = this.jobs.get(jobId)
    if (!buf) return
    let any = false
    for (const l of [...lines].sort((a, b) => a.seq - b.seq)) {
      if (this.insert(buf, { seq: l.seq, line: l.line })) any = true
    }
    if (any) this.changed(buf)
  }

  private insert(buf: JobBuffer, item: JobLine): boolean {
    const a = buf.lines
    const last = a.length ? a[a.length - 1].seq : 0
    if (item.seq > last) {
      a.push(item)
    } else {
      let lo = 0
      let hi = a.length
      while (lo < hi) {
        const mid = (lo + hi) >> 1
        if (a[mid].seq < item.seq) lo = mid + 1
        else hi = mid
      }
      if (a[lo]?.seq === item.seq) return false
      a.splice(lo, 0, item)
    }
    if (a.length > MAX_JOB_LINES) a.splice(0, a.length - MAX_JOB_LINES)
    return true
  }

  private changed(buf: JobBuffer) {
    const lines = buf.lines.slice()
    buf.snapshot = {
      lines,
      lastSeq: lines.length ? lines[lines.length - 1].seq : 0,
      finished: buf.finished,
    }
    this.batcher.notify()
  }
}

export function metricKey(profile: string, scope: string) {
  return `${profile}|${scope}`
}

export class MetricStore {
  private batcher = new Batcher()
  private latest = new Map<string, MetricEvent>()

  subscribe = this.batcher.subscribe

  record(e: MetricEvent) {
    this.latest.set(metricKey(e.profile, e.scope), e)
    this.batcher.notify()
  }

  getSnapshot(profile: string, scope: string): MetricEvent | undefined {
    return this.latest.get(metricKey(profile, scope))
  }
}
