import { useSyncExternalStore } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/lib/api/client'
import { api } from '@/lib/api/endpoints'
import type { Container, Metrics, MetricPoint } from '@/lib/api/types'
import { EventStreamTestProvider } from '@/lib/events/EventStreamTestProvider'
import { createFakeEventStream, type FakeEventStream } from '@/lib/events/testing'
import { expectNoA11yViolations } from '@/test/axe'
import { profileFixture } from '@/test/fixtures'
import type { UPlotChartProps } from './UPlotChart'

const chartProps: UPlotChartProps[] = []
vi.mock('./UPlotChart', () => ({
  UPlotChart: (props: UPlotChartProps) => {
    chartProps.push(props)
    const n = useSyncExternalStore(props.buffer.subscribe, () => props.buffer.version)
    return (
      <div data-testid={`chart-${props.seriesKey}`} data-limit={String(props.limit)}>
        {`v${n}:${JSON.stringify(props.buffer.column(props.seriesKey).values)}`}
      </div>
    )
  },
}))

import { CapacityView } from './CapacityView'

const point = (sec: number, over: Partial<MetricPoint> = {}): MetricPoint => ({
  takenAt: new Date(Date.UTC(2026, 0, 1, 0, 0, sec)).toISOString(),
  cpuPercent: 50,
  memoryBytes: 2 * 1024 ** 3,
  diskBytes: 10 * 1024 ** 3,
  ...over,
})
const metrics = (points: MetricPoint[]): Metrics => ({
  profile: 'synthetic-a',
  scope: 'vm',
  from: '',
  to: '',
  points,
})
const container = (over: Partial<Container> = {}): Container => ({
  id: 'c1',
  name: 'synthetic-web',
  image: 'synthetic:1',
  state: 'running',
  cpuPercent: 10,
  memoryBytes: 1024 ** 3,
  status: 'Up 2 hours',
  createdAt: '2026-01-01T00:00:00Z',
  ports: [],
  ...over,
})

let fake: FakeEventStream
let qc: QueryClient

function renderView() {
  return render(
    <QueryClientProvider client={qc}>
      <EventStreamTestProvider client={fake}>
        <MemoryRouter initialEntries={['/profiles/synthetic-a/capacity']}>
          <Routes>
            <Route path="/profiles/:name/capacity" element={<CapacityView />} />
          </Routes>
        </MemoryRouter>
      </EventStreamTestProvider>
    </QueryClientProvider>,
  )
}

function mockApi(opts: { status?: 'running' | 'stopped'; points?: MetricPoint[] } = {}) {
  vi.spyOn(api, 'listProfiles').mockResolvedValue({
    profiles: [profileFixture({ status: opts.status ?? 'running' })],
    warnings: [],
  } as never)
  const getMetrics = vi
    .spyOn(api, 'getMetrics')
    .mockResolvedValue(metrics(opts.points ?? [point(0), point(10)]))
  const getContainers = vi
    .spyOn(api, 'getContainers')
    .mockResolvedValue({ containers: [container()] })
  return { getMetrics, getContainers }
}

beforeEach(() => {
  chartProps.length = 0
  fake = createFakeEventStream()
  qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
})
afterEach(() => vi.restoreAllMocks())

describe('CapacityView', () => {
  it('loads history and draws three charts with the allocated limits', async () => {
    const { getMetrics } = mockApi()
    renderView()
    expect(await screen.findByTestId('chart-cpu')).toHaveAttribute('data-limit', '200')
    expect(screen.getByTestId('chart-mem')).toHaveAttribute('data-limit', String(4 * 1024 ** 3))
    expect(screen.getByTestId('chart-disk')).toHaveAttribute('data-limit', String(60 * 1024 ** 3))
    expect(screen.getByRole('heading', { name: 'Container memory' })).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Docker disk' })).toBeInTheDocument()
    const [, params] = getMetrics.mock.calls[0]
    expect(params).toMatchObject({ scope: 'vm', maxPoints: 600 })
    expect(Date.parse(params!.to!) - Date.parse(params!.from!)).toBe(3_600_000)
  })

  it('labels each chart with the latest value, the limit and the percentage', async () => {
    mockApi()
    renderView()
    const fig = (await screen.findAllByRole('img'))[0]
    expect(fig).toHaveAccessibleName('CPU: 50.0% used of 200.0% allocated (25%).')
  })

  it('extends the charts with live events without a refetch', async () => {
    const { getMetrics } = mockApi()
    renderView()
    await screen.findByTestId('chart-cpu')
    expect(screen.getByTestId('chart-cpu')).toHaveTextContent('[50,50]')
    act(() =>
      fake.emit('metric', {
        profile: 'synthetic-a',
        scope: 'vm',
        takenAt: point(20).takenAt,
        cpuPercent: 75,
        memoryBytes: 1,
        diskBytes: null,
      }),
    )
    await waitFor(() => expect(screen.getByTestId('chart-cpu')).toHaveTextContent('[50,50,75]'))
    expect(screen.getByTestId('chart-disk')).toHaveTextContent('[10737418240,10737418240,null]')
    expect(getMetrics).toHaveBeenCalledTimes(1)
  })

  it('ignores events for a container scope', async () => {
    mockApi()
    renderView()
    await screen.findByTestId('chart-cpu')
    act(() =>
      fake.emit('metric', {
        profile: 'synthetic-a',
        scope: 'c1',
        takenAt: point(20).takenAt,
        cpuPercent: 99,
        memoryBytes: 1,
        diskBytes: 1,
      }),
    )
    expect(screen.getByTestId('chart-cpu')).toHaveTextContent('[50,50]')
  })

  it('keeps null as a gap and describes it as no reading', async () => {
    mockApi({ points: [point(0), point(10, { cpuPercent: null })] })
    renderView()
    expect(await screen.findByTestId('chart-cpu')).toHaveTextContent('[50,null]')
    expect((await screen.findAllByRole('img'))[0]).toHaveAccessibleName(/no reading yet/)
  })

  it('requests a new window when the range changes', async () => {
    const { getMetrics } = mockApi()
    renderView()
    await screen.findByTestId('chart-cpu')
    await userEvent.selectOptions(screen.getByLabelText('Time range'), '24h')
    await waitFor(() => expect(getMetrics).toHaveBeenCalledTimes(2))
    const [, params] = getMetrics.mock.calls[1]
    expect(Date.parse(params!.to!) - Date.parse(params!.from!)).toBe(24 * 3_600_000)
  })

  it('shows the data table on request', async () => {
    mockApi()
    renderView()
    await screen.findByTestId('chart-cpu')
    expect(screen.queryByRole('table', { name: 'CPU samples' })).toBeNull()
    await userEvent.click(screen.getAllByRole('button', { name: 'Show data' })[0])
    const table = screen.getByRole('table', { name: 'CPU samples' })
    expect(within(table).getAllByRole('row')).toHaveLength(3)
    expect(within(table).getAllByText('50.0%')).toHaveLength(2)
  })

  it('shows the empty state when there are no samples', async () => {
    mockApi({ points: [] })
    renderView()
    expect(await screen.findByText(/No samples in this range yet/)).toBeInTheDocument()
    expect(screen.queryByTestId('chart-cpu')).toBeNull()
  })

  it('shows the stopped state with history and no container request', async () => {
    const { getContainers } = mockApi({ status: 'stopped' })
    renderView()
    expect(await screen.findByTestId('chart-cpu')).toBeInTheDocument()
    expect(screen.getByText(/The VM is stopped. The charts show recorded history/)).toBeInTheDocument()
    expect(screen.getByText(/no container data/)).toBeInTheDocument()
    expect(getContainers).not.toHaveBeenCalled()
  })

  it('shows an error when the history cannot be loaded', async () => {
    mockApi()
    vi.spyOn(api, 'getMetrics').mockRejectedValue(new ApiError(404, 'not_found', 'nope'))
    renderView()
    expect(await screen.findByRole('alert')).toHaveTextContent('Could not load the metric history')
  })

  it('lists containers sorted by memory with a total next to the VM figure', async () => {
    mockApi()
    vi.spyOn(api, 'getContainers').mockResolvedValue({
      containers: [
        container({ id: 'a', name: 'small', memoryBytes: 1024 ** 2, cpuPercent: 1 }),
        container({ id: 'b', name: 'big', memoryBytes: 1024 ** 3, cpuPercent: 30 }),
      ],
    })
    renderView()
    const table = await screen.findByRole('table', { name: 'Usage per container' })
    const names = within(table)
      .getAllByRole('rowheader')
      .map((r) => r.textContent)
    expect(names.slice(0, 2)).toEqual(['big', 'small'])
    expect(within(table).getByText('Containers total').closest('tr')).toHaveTextContent('31.0%')
    expect(within(table).getByText('Latest VM sample').closest('tr')).toHaveTextContent('50.0%')
  })

  it.each([
    [409, 'invalid_state', /VM is stopped/],
    [502, 'docker_unavailable', /Docker is not answering/],
  ])('renders a %i %s container answer as a state, not an error', async (status, code, text) => {
    mockApi()
    vi.spyOn(api, 'getContainers').mockRejectedValue(new ApiError(status, code, 'x'))
    renderView()
    expect(await screen.findByText(text)).toBeInTheDocument()
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('polls the containers every 5 seconds while running', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      const { getContainers } = mockApi()
      renderView()
      await screen.findByRole('table', { name: 'Usage per container' })
      const before = getContainers.mock.calls.length
      await act(async () => {
        await vi.advanceTimersByTimeAsync(5_100)
      })
      expect(getContainers.mock.calls.length).toBeGreaterThan(before)
    } finally {
      vi.useRealTimers()
    }
  })

  it('has no accessibility violations once loaded', async () => {
    mockApi()
    const { container: root } = renderView()
    await screen.findByTestId('chart-cpu')
    await userEvent.click(screen.getAllByRole('button', { name: 'Show data' })[0])
    await expectNoA11yViolations(root)
  })
})
