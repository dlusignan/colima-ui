// Test helpers: a scriptable stream response and a fake client.

import type { ConnectionState, EventFilter, EventMap, EventStreamSource } from './types'

export interface StubStream {
  response: Response
  push(chunk: string | Uint8Array): void
  end(): void
  error(e: unknown): void
}

export function makeStream(status = 200): StubStream {
  let ctrl!: ReadableStreamDefaultController<Uint8Array>
  const body = new ReadableStream<Uint8Array>({
    start(c) {
      ctrl = c
    },
  })
  const enc = new TextEncoder()
  return {
    response: new Response(status === 200 ? body : null, {
      status,
      headers: { 'content-type': 'text/event-stream' },
    }),
    push: (chunk) => ctrl.enqueue(typeof chunk === 'string' ? enc.encode(chunk) : chunk),
    end: () => ctrl.close(),
    error: (e) => ctrl.error(e),
  }
}

export function sseFrame(event: string, payload: unknown, id = 1): string {
  return `id: ${id}\nevent: ${event}\ndata: ${JSON.stringify(payload)}\n\n`
}

export interface FakeEventStream extends EventStreamSource {
  emit<K extends keyof EventMap>(type: K, event: EventMap[K]): void
  setState(state: ConnectionState): void
  /** Simulates a reconnect: epoch +1 and resync callbacks. */
  reconnect(): void
  connectCalls: number
  closeCalls: number
}

export function createFakeEventStream(): FakeEventStream {
  let state: ConnectionState = 'open'
  let epoch = 0
  const entries = new Set<{
    type: string
    listener: (e: never) => void
    filter?: EventFilter
  }>()
  const resyncs = new Set<() => void>()
  const states = new Set<() => void>()
  const fake: FakeEventStream = {
    connectCalls: 0,
    closeCalls: 0,
    connect() {
      fake.connectCalls++
    },
    close() {
      fake.closeCalls++
    },
    getState: () => state,
    getEpoch: () => epoch,
    subscribe(type, listener, filter) {
      const entry = { type, listener: listener as (e: never) => void, filter }
      entries.add(entry)
      return () => {
        entries.delete(entry)
      }
    },
    onResync(cb) {
      resyncs.add(cb)
      return () => {
        resyncs.delete(cb)
      }
    },
    onStateChange(cb) {
      states.add(cb)
      return () => {
        states.delete(cb)
      }
    },
    emit(type, event) {
      const p = event as unknown as Record<string, unknown>
      for (const e of [...entries]) {
        if (e.type !== type) continue
        const f = e.filter
        if (f?.profile !== undefined && p.profile !== f.profile) continue
        if (f?.jobId !== undefined && type !== 'metric' && p.jobId !== f.jobId) continue
        if (f?.scope !== undefined && type === 'metric' && p.scope !== f.scope) continue
        e.listener(event as never)
      }
    },
    setState(next) {
      state = next
      states.forEach((l) => l())
    },
    reconnect() {
      epoch++
      states.forEach((l) => l())
      resyncs.forEach((l) => l())
    },
  }
  return fake
}
