import { StrictMode, type ReactNode } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, render, renderHook, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { queryKeys } from '@/lib/api/keys'
import { setTokenForTests } from '@/lib/auth/token'
import { EventStreamProvider } from './EventStreamProvider'
import {
  useConnectionEpoch,
  useConnectionState,
  useEventListener,
  useJobOutput,
  useLatestMetric,
} from './hooks'
import { EventStreamTestProvider } from './EventStreamTestProvider'
import { createFakeEventStream, makeStream, sseFrame } from './testing'

const metric = (cpu: number, scope = 'vm') => ({
  profile: 'default',
  scope,
  takenAt: '2026-01-01T00:00:00Z',
  cpuPercent: cpu,
  memoryBytes: 2,
  diskBytes: null,
})
const finished = {
  jobId: 'j1',
  profile: 'default',
  state: 'succeeded' as const,
  finishedAt: '2026-01-01T00:00:00Z',
  exitCode: 0,
}

let qc: QueryClient
const fake = () => createFakeEventStream()
function wrapperFor(client: ReturnType<typeof fake>) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>
      <EventStreamTestProvider client={client}>{children}</EventStreamTestProvider>
    </QueryClientProvider>
  )
}

beforeEach(() => {
  qc = new QueryClient()
})

describe('hooks with a fake stream', () => {
  it('useConnectionState and useConnectionEpoch follow the client', () => {
    const c = fake()
    const { result } = renderHook(() => [useConnectionState(), useConnectionEpoch()] as const, {
      wrapper: wrapperFor(c),
    })
    expect(result.current).toEqual(['open', 0])
    act(() => c.setState('reconnecting'))
    expect(result.current[0]).toBe('reconnecting')
    act(() => c.reconnect())
    expect(result.current[1]).toBe(1)
  })

  it('useEventListener delivers filtered events, uses the latest callback and unsubscribes', () => {
    const c = fake()
    const calls: number[] = []
    const { rerender, unmount } = renderHook(
      ({ tag }) =>
        useEventListener('metric', (e) => calls.push(e.cpuPercent + tag), {
          scope: 'vm',
        }),
      { wrapper: wrapperFor(c), initialProps: { tag: 0 } },
    )
    act(() => c.emit('metric', metric(1)))
    act(() => c.emit('metric', metric(9, 'abc')))
    rerender({ tag: 100 })
    act(() => c.emit('metric', metric(2)))
    expect(calls).toEqual([1, 102])
    unmount()
    act(() => c.emit('metric', metric(3)))
    expect(calls).toHaveLength(2)
  })

  it('useLatestMetric keeps the latest per scope', async () => {
    const c = fake()
    const { result } = renderHook(() => useLatestMetric('default'), {
      wrapper: wrapperFor(c),
    })
    expect(result.current).toBeUndefined()
    act(() => {
      c.emit('metric', metric(1))
      c.emit('metric', metric(2))
      c.emit('metric', metric(50, 'abc'))
    })
    await waitFor(() => expect(result.current?.cpuPercent).toBe(2))
  })

  it('useJobOutput buffers, dedupes by seq, seeds from REST and records finish', async () => {
    const c = fake()
    const { result, unmount } = renderHook(() => useJobOutput('j1'), {
      wrapper: wrapperFor(c),
    })
    const out = (seq: number, line = `l${seq}`) =>
      c.emit('job.output', { jobId: 'j1', profile: 'default', seq, line })
    act(() => {
      out(3)
      out(4)
      c.emit('job.output', {
        jobId: 'other',
        profile: 'default',
        seq: 1,
        line: 'x',
      })
    })
    await waitFor(() => expect(result.current.lastSeq).toBe(4))
    act(() => {
      result.current.seed([
        { seq: 1, line: 'l1' },
        { seq: 2, line: 'l2' },
        { seq: 3, line: 'dup' },
      ])
    })
    await waitFor(() => expect(result.current.lines.map((l) => l.seq)).toEqual([1, 2, 3, 4]))
    expect(result.current.lines[2].line).toBe('l3')
    act(() => c.emit('job.finished', finished))
    await waitFor(() => expect(result.current.finished).toEqual(finished))
    unmount()
    // buffer freed: a new subscriber starts empty
    const again = renderHook(() => useJobOutput('j1'), {
      wrapper: wrapperFor(c),
    })
    expect(again.result.current.lines).toEqual([])
  })

  it('batches a burst of lines into one notification', async () => {
    const c = fake()
    let renders = 0
    renderHook(
      () => {
        renders++
        return useJobOutput('j1')
      },
      { wrapper: wrapperFor(c) },
    )
    const before = renders
    act(() => {
      for (let i = 1; i <= 500; i++) {
        c.emit('job.output', {
          jobId: 'j1',
          profile: 'default',
          seq: i,
          line: 'x',
        })
      }
    })
    await waitFor(() => expect(renders).toBeGreaterThan(before))
    expect(renders - before).toBeLessThanOrEqual(2)
  })

  it('job.finished invalidates the job, the profiles; resync invalidates profiles and jobs', () => {
    const c = fake()
    const spy = vi.spyOn(qc, 'invalidateQueries')
    renderHook(() => null, { wrapper: wrapperFor(c) })
    act(() => c.emit('job.finished', finished))
    const keys = spy.mock.calls.map((x) => x[0]?.queryKey)
    expect(keys).toContainEqual(queryKeys.job('j1'))
    expect(keys).toContainEqual(queryKeys.profiles)
    spy.mockClear()
    act(() => c.reconnect())
    const k2 = spy.mock.calls.map((x) => x[0]?.queryKey)
    expect(k2).toContainEqual(queryKeys.profiles)
    expect(k2).toContainEqual(['jobs'])
  })
})

describe('EventStreamProvider with the real client', () => {
  let fetchMock: ReturnType<typeof vi.fn>
  let signals: AbortSignal[]

  beforeEach(() => {
    signals = []
    fetchMock = vi.fn((_u: string, init: RequestInit) => {
      signals.push(init.signal as AbortSignal)
      return Promise.resolve(makeStream().response)
    })
    vi.stubGlobal('fetch', fetchMock)
    setTokenForTests('synthetic-token')
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
    setTokenForTests(null)
  })

  function Consumer({ n }: { n: number }) {
    useConnectionState()
    useLatestMetric('default')
    useJobOutput(`j${n}`)
    return <span>consumer</span>
  }

  const tree = (children: ReactNode, strict = false) => {
    const el = (
      <QueryClientProvider client={qc}>
        <EventStreamProvider>{children}</EventStreamProvider>
      </QueryClientProvider>
    )
    return strict ? <StrictMode>{el}</StrictMode> : el
  }

  it('opens one connection for many consumers and closes it on unmount', async () => {
    const { unmount } = render(
      tree(
        <>
          {[1, 2, 3, 4, 5].map((n) => (
            <Consumer key={n} n={n} />
          ))}
        </>,
      ),
    )
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))
    expect(screen.getAllByText('consumer')).toHaveLength(5)
    expect(signals[0].aborted).toBe(false)
    unmount()
    expect(signals[0].aborted).toBe(true)
    await new Promise((r) => setTimeout(r, 50))
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('leaves at most one live connection under StrictMode', async () => {
    const { unmount } = render(tree(<Consumer n={1} />, true))
    await waitFor(() => expect(fetchMock).toHaveBeenCalled())
    await new Promise((r) => setTimeout(r, 50))
    expect(signals.filter((s) => !s.aborted)).toHaveLength(1)
    unmount()
    expect(signals.every((s) => s.aborted)).toBe(true)
  })

  it('streams a real frame into a hook', async () => {
    const s = makeStream()
    fetchMock.mockImplementationOnce((_u: string, init: RequestInit) => {
      signals.push(init.signal as AbortSignal)
      return Promise.resolve(s.response)
    })
    const { result } = renderHook(() => useLatestMetric('default'), {
      wrapper: ({ children }) => tree(children),
    })
    await waitFor(() => expect(fetchMock).toHaveBeenCalled())
    await act(async () => {
      s.push(sseFrame('metric', metric(7)))
      await new Promise((r) => setTimeout(r, 40))
    })
    await waitFor(() => expect(result.current?.cpuPercent).toBe(7))
  })
})
