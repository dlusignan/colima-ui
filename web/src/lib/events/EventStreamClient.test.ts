import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { getAuthStatus, setTokenForTests } from '@/lib/auth/token'
import { EventStreamClient } from './EventStreamClient'
import { makeStream, sseFrame, type StubStream } from './testing'

const metric = {
  profile: 'default',
  scope: 'vm',
  takenAt: '2026-01-01T00:00:00Z',
  cpuPercent: 1,
  memoryBytes: 2,
  diskBytes: null,
}

let streams: StubStream[]
let fetchMock: ReturnType<typeof vi.fn>

function queue(...items: (StubStream | Error)[]) {
  fetchMock.mockImplementation(() => {
    const next = items.shift()
    if (!next) return Promise.reject(new Error('no more'))
    if (next instanceof Error) return Promise.reject(next)
    streams.push(next)
    return Promise.resolve(next.response)
  })
}

const tick = (ms = 0) => vi.advanceTimersByTimeAsync(ms)
const mk = () => new EventStreamClient({ random: () => 1 })

beforeEach(() => {
  vi.useFakeTimers()
  streams = []
  fetchMock = vi.fn()
  vi.stubGlobal('fetch', fetchMock)
  setTokenForTests('synthetic-token')
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
  setTokenForTests(null)
})

describe('EventStreamClient', () => {
  it('connects once with the bearer header and delivers typed, filtered events', async () => {
    const s = makeStream()
    queue(s)
    const c = mk()
    const all: unknown[] = []
    const other: unknown[] = []
    c.subscribe('metric', (e) => all.push(e))
    c.subscribe('metric', (e) => other.push(e), { profile: 'other' })
    c.connect()
    c.connect()
    await tick()
    expect(fetchMock).toHaveBeenCalledTimes(1)
    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe('/api/events')
    expect(init.headers.Authorization).toBe('Bearer synthetic-token')
    expect(c.getState()).toBe('open')
    s.push(sseFrame('metric', metric) + sseFrame('unknown', {}) + 'event: metric\ndata: {bad\n\n')
    await tick()
    expect(all).toEqual([metric])
    expect(other).toEqual([])
    c.close()
  })

  it('backs off exponentially up to the cap and resets after activity', async () => {
    queue(new Error('x'), new Error('x'), new Error('x'), new Error('x'))
    const c = new EventStreamClient({ random: () => 1, maxDelayMs: 1500 })
    c.connect()
    await tick()
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(c.getState()).toBe('reconnecting')
    await tick(499)
    expect(fetchMock).toHaveBeenCalledTimes(1)
    await tick(1)
    expect(fetchMock).toHaveBeenCalledTimes(2)
    await tick(1000)
    expect(fetchMock).toHaveBeenCalledTimes(3)
    await tick(1499)
    expect(fetchMock).toHaveBeenCalledTimes(3)
    await tick(1)
    expect(fetchMock).toHaveBeenCalledTimes(4)
    c.close()
  })

  it('does not reset on headers only, resets after a comment line', async () => {
    const a = makeStream()
    const b = makeStream()
    const d = makeStream()
    queue(new Error('x'), a, b, d)
    const c = mk()
    c.connect()
    await tick(500) // 2nd fetch (a), next delay would be 1000
    a.end()
    await tick() // closed without activity
    await tick(1000)
    expect(fetchMock).toHaveBeenCalledTimes(3) // b after 1000 ms
    b.push(': keepalive\n')
    await tick()
    b.end()
    await tick()
    await tick(499)
    expect(fetchMock).toHaveBeenCalledTimes(3)
    await tick(1)
    expect(fetchMock).toHaveBeenCalledTimes(4)
    c.close()
  })

  it('reconnects after a clean end and signals resync only on the second open', async () => {
    const a = makeStream()
    const b = makeStream()
    queue(a, b)
    const c = mk()
    const resync = vi.fn()
    c.onResync(resync)
    c.connect()
    await tick()
    expect(resync).not.toHaveBeenCalled()
    expect(c.getEpoch()).toBe(0)
    a.end()
    await tick()
    expect(c.getState()).toBe('reconnecting')
    await tick(500)
    expect(c.getState()).toBe('open')
    expect(resync).toHaveBeenCalledTimes(1)
    expect(c.getEpoch()).toBe(1)
    c.close()
  })

  it('aborts and reconnects after the watchdog window of silence', async () => {
    const a = makeStream()
    const b = makeStream()
    queue(a, b)
    const c = mk()
    c.connect()
    await tick()
    a.push(': keepalive\n\n')
    await tick(44_000)
    expect(fetchMock.mock.calls[0][1].signal.aborted).toBe(false)
    await tick(1_001)
    expect(fetchMock.mock.calls[0][1].signal.aborted).toBe(true)
    await tick(500)
    expect(fetchMock).toHaveBeenCalledTimes(2)
    c.close()
  })

  it('401 marks the token rejected and stops; 403 stops without retry', async () => {
    queue(makeStream(401))
    const c = mk()
    c.connect()
    await tick()
    expect(getAuthStatus()).toBe('rejected')
    expect(c.getState()).toBe('closed')
    await tick(60_000)
    expect(fetchMock).toHaveBeenCalledTimes(1)

    setTokenForTests('synthetic-token')
    fetchMock.mockClear()
    queue(makeStream(403))
    const c2 = mk()
    c2.connect()
    await tick(60_000)
    expect(c2.getState()).toBe('closed')
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('retries on a 5xx answer', async () => {
    queue(makeStream(503), makeStream())
    const c = mk()
    c.connect()
    await tick(500)
    expect(fetchMock).toHaveBeenCalledTimes(2)
    expect(c.getState()).toBe('open')
    c.close()
  })

  it('close aborts, never retries, and connect works again afterwards', async () => {
    const a = makeStream()
    const b = makeStream()
    queue(a, b)
    const c = mk()
    c.connect()
    await tick()
    c.close()
    expect(fetchMock.mock.calls[0][1].signal.aborted).toBe(true)
    expect(c.getState()).toBe('closed')
    await tick(60_000)
    expect(fetchMock).toHaveBeenCalledTimes(1)
    c.connect()
    await tick()
    expect(fetchMock).toHaveBeenCalledTimes(2)
    expect(c.getState()).toBe('open')
    c.close()
  })

  it('an attempt superseded while fetching never writes state', async () => {
    let resolve!: (r: Response) => void
    fetchMock.mockImplementationOnce(() => new Promise<Response>((r) => (resolve = r)))
    const c = mk()
    c.connect()
    c.close()
    resolve(makeStream().response)
    await tick()
    expect(c.getState()).toBe('closed')
  })

  it('without a token it does not fetch', async () => {
    setTokenForTests(null)
    const c = mk()
    c.connect()
    await tick()
    expect(fetchMock).not.toHaveBeenCalled()
    expect(c.getState()).toBe('closed')
  })
})
