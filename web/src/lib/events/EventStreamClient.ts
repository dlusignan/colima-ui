// The single Server Sent Events connection. Plain TypeScript, no React.
//
// `EventSource` cannot send an Authorization header, so this reads
// `fetch('/api/events')` as a stream (docs/api.md section 5.1).

import { getToken, markTokenRejected } from '@/lib/auth/token'
import { SseParser } from './parseSse'
import type { ConnectionState, EventFilter, EventMap, EventStreamSource } from './types'

export interface EventStreamClientOptions {
  url?: string
  initialDelayMs?: number
  maxDelayMs?: number
  /** No bytes for this long (the server keepalive is 15 s) aborts and reconnects. */
  watchdogMs?: number
  random?: () => number
}

interface Entry {
  type: keyof EventMap
  listener: (event: never) => void
  filter?: EventFilter
}

const KNOWN: ReadonlySet<string> = new Set(['job.output', 'job.finished', 'metric'])

function matches(type: keyof EventMap, payload: Record<string, unknown>, f?: EventFilter) {
  if (!f) return true
  if (f.profile !== undefined && payload.profile !== f.profile) return false
  if (f.jobId !== undefined && type !== 'metric' && payload.jobId !== f.jobId) return false
  if (f.scope !== undefined && type === 'metric' && payload.scope !== f.scope) return false
  return true
}

export class EventStreamClient implements EventStreamSource {
  private readonly url: string
  private readonly initialDelay: number
  private readonly maxDelay: number
  private readonly watchdogMs: number
  private readonly random: () => number

  private state: ConnectionState = 'connecting'
  private epoch = 0
  private everOpened = false
  private running = false
  private attemptId = 0
  private backoff: number
  private controller: AbortController | null = null
  private retryTimer: ReturnType<typeof setTimeout> | null = null
  private watchdogTimer: ReturnType<typeof setTimeout> | null = null
  private entries = new Set<Entry>()
  private resyncs = new Set<() => void>()
  private stateListeners = new Set<() => void>()

  constructor(options: EventStreamClientOptions = {}) {
    this.url = options.url ?? '/api/events'
    this.initialDelay = options.initialDelayMs ?? 500
    this.maxDelay = options.maxDelayMs ?? 15_000
    this.watchdogMs = options.watchdogMs ?? 45_000
    this.random = options.random ?? Math.random
    this.backoff = this.initialDelay
  }

  getState(): ConnectionState {
    return this.state
  }

  getEpoch(): number {
    return this.epoch
  }

  connect(): void {
    if (this.running) return
    this.running = true
    this.backoff = this.initialDelay
    this.setState(this.everOpened ? 'reconnecting' : 'connecting')
    void this.attempt()
  }

  close(): void {
    this.running = false
    this.attemptId++
    this.clearTimers()
    this.controller?.abort()
    this.controller = null
    this.setState('closed')
  }

  subscribe<K extends keyof EventMap>(
    type: K,
    listener: (event: EventMap[K]) => void,
    filter?: EventFilter,
  ): () => void {
    const entry: Entry = {
      type,
      listener: listener as (e: never) => void,
      filter,
    }
    this.entries.add(entry)
    return () => {
      this.entries.delete(entry)
    }
  }

  onResync(callback: () => void): () => void {
    this.resyncs.add(callback)
    return () => {
      this.resyncs.delete(callback)
    }
  }

  onStateChange(callback: () => void): () => void {
    this.stateListeners.add(callback)
    return () => {
      this.stateListeners.delete(callback)
    }
  }

  private setState(next: ConnectionState) {
    if (next === this.state) return
    this.state = next
    this.stateListeners.forEach((l) => l())
  }

  private clearTimers() {
    if (this.retryTimer) clearTimeout(this.retryTimer)
    if (this.watchdogTimer) clearTimeout(this.watchdogTimer)
    this.retryTimer = null
    this.watchdogTimer = null
  }

  private stale(id: number) {
    return !this.running || id !== this.attemptId
  }

  private fatal() {
    this.running = false
    this.attemptId++
    this.clearTimers()
    this.setState('closed')
  }

  private scheduleRetry() {
    if (!this.running) return
    this.clearTimers()
    this.controller = null
    this.setState('reconnecting')
    const base = this.backoff
    this.backoff = Math.min(this.backoff * 2, this.maxDelay)
    const delay = base / 2 + this.random() * (base / 2)
    this.retryTimer = setTimeout(() => {
      this.retryTimer = null
      void this.attempt()
    }, delay)
  }

  private armWatchdog(controller: AbortController) {
    if (this.watchdogTimer) clearTimeout(this.watchdogTimer)
    this.watchdogTimer = setTimeout(() => controller.abort(), this.watchdogMs)
  }

  private async attempt(): Promise<void> {
    const id = ++this.attemptId
    const token = getToken()
    if (!token) {
      this.fatal()
      return
    }
    const controller = new AbortController()
    this.controller = controller

    let res: Response
    try {
      res = await fetch(this.url, {
        headers: {
          Authorization: `Bearer ${token}`,
          Accept: 'text/event-stream',
        },
        signal: controller.signal,
        cache: 'no-store',
      })
    } catch {
      if (!this.stale(id)) this.scheduleRetry()
      return
    }
    if (this.stale(id)) {
      controller.abort()
      return
    }
    if (res.status === 401) {
      markTokenRejected()
      this.fatal()
      return
    }
    if (res.status === 403) {
      this.fatal()
      return
    }
    if (!res.ok || !res.body) {
      this.scheduleRetry()
      return
    }

    const reconnected = this.everOpened
    this.everOpened = true
    if (reconnected) this.epoch++
    this.setState('open')
    if (reconnected) this.resyncs.forEach((cb) => cb())

    const reader = res.body.getReader()
    controller.signal.addEventListener('abort', () => {
      reader.cancel().catch(() => {})
    })
    const parser = new SseParser()
    let seenActivity = 0
    this.armWatchdog(controller)
    try {
      for (;;) {
        const { done, value } = await reader.read()
        if (this.stale(id)) return
        if (done) break
        this.armWatchdog(controller)
        for (const frame of parser.push(value)) this.dispatch(frame.event, frame.data)
        if (parser.activity !== seenActivity) {
          seenActivity = parser.activity
          this.backoff = this.initialDelay
        }
      }
    } catch {
      // fall through to the retry
    }
    parser.end()
    if (!this.stale(id)) this.scheduleRetry()
  }

  private dispatch(event: string, data: string) {
    if (!KNOWN.has(event)) return
    let payload: unknown
    try {
      payload = JSON.parse(data)
    } catch {
      console.warn(`Dropped an unparseable "${event}" event.`)
      return
    }
    if (typeof payload !== 'object' || payload === null) return
    const type = event as keyof EventMap
    for (const entry of [...this.entries]) {
      if (entry.type === type && matches(type, payload as Record<string, unknown>, entry.filter)) {
        entry.listener(payload as never)
      }
    }
  }
}
