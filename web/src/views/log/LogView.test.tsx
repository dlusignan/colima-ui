import type { ReactNode } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/lib/api/client'
import { api } from '@/lib/api/endpoints'
import type { Job } from '@/lib/api/types'
import { EventStreamTestProvider } from '@/lib/events/EventStreamTestProvider'
import { createFakeEventStream, type FakeEventStream } from '@/lib/events/testing'

const written: string[][] = []
vi.mock('./TerminalPane', () => ({
  TerminalPane: ({ lines, notice }: { lines: { seq: number; line: string }[]; notice?: string }) => {
    written.push(lines.map((l) => l.line))
    return <pre data-testid="pane">{`${notice ?? ''}|${lines.map((l) => l.line).join('\n')}`}</pre>
  },
}))

import { LogView } from './LogView'

const ESC = '\x1b[32m'
const job = (over: Partial<Job> = {}): Job => ({
  id: 'j1',
  profile: 'default',
  action: 'start',
  state: 'running',
  startedAt: '2026-01-01T00:00:00Z',
  finishedAt: null,
  exitCode: null,
  output: [
    { seq: 1, line: 'first' },
    { seq: 2, line: `${ESC}second\x1b[0m` },
  ],
  truncated: false,
  droppedLines: 0,
  ...over,
})

let fake: FakeEventStream
let qc: QueryClient

function renderAt(url: string, ui: ReactNode = <LogView />) {
  return render(
    <QueryClientProvider client={qc}>
      <EventStreamTestProvider client={fake}>
        <MemoryRouter initialEntries={[url]}>
          <Routes>
            <Route path="/profiles/:name/log" element={ui} />
            <Route path="/profiles/:name" element={<p>profile page</p>} />
          </Routes>
        </MemoryRouter>
      </EventStreamTestProvider>
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  written.length = 0
  fake = createFakeEventStream()
  qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
})
afterEach(() => vi.restoreAllMocks())

const pane = () => screen.getByTestId('pane').textContent

describe('LogView', () => {
  it('shows earlier output first and then the recorded stream with colors intact', async () => {
    vi.spyOn(api, 'getJob').mockResolvedValue(job())
    renderAt('/profiles/default/log?job=j1')
    await waitFor(() => expect(pane()).toBe(`|first\n${ESC}second\x1b[0m`))
    const base = { jobId: 'j1', profile: 'default' }
    act(() => {
      fake.emit('job.output', { ...base, seq: 2, line: 'dup' })
      fake.emit('job.output', { ...base, seq: 3, line: '\x1b[31mred\x1b[0m' })
      fake.emit('job.output', { ...base, seq: 4, line: 'tail' })
    })
    await waitFor(() => expect(pane()).toContain('tail'))
    expect(pane()).toBe(`|first\n${ESC}second\x1b[0m\n\x1b[31mred\x1b[0m\ntail`)
    expect(screen.getByTestId('job-state')).toHaveTextContent('running')
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeInTheDocument()
  })

  it('keeps live lines that arrive before the job fetch resolves, in order', async () => {
    let resolve!: (j: Job) => void
    vi.spyOn(api, 'getJob').mockReturnValue(new Promise<Job>((r) => (resolve = r)))
    renderAt('/profiles/default/log?job=j1')
    act(() => fake.emit('job.output', { jobId: 'j1', profile: 'default', seq: 3, line: 'early live' }))
    await act(async () => resolve(job()))
    await waitFor(() => expect(pane()).toBe(`|first\n${ESC}second\x1b[0m\nearly live`))
  })

  it('shows a finished job with output, exit code and no cancel button', async () => {
    vi.spyOn(api, 'getJob').mockResolvedValue(
      job({ state: 'failed', exitCode: 1, finishedAt: '2026-01-01T00:01:00Z' }),
    )
    renderAt('/profiles/default/log?job=j1')
    await waitFor(() => expect(screen.getByTestId('job-state')).toHaveTextContent('failed'))
    expect(screen.getByText('exit 1')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Cancel' })).toBeNull()
    expect(pane()).toContain('first')
  })

  it('applies the finished event without waiting for a refetch', async () => {
    vi.spyOn(api, 'getJob').mockResolvedValue(job())
    renderAt('/profiles/default/log?job=j1')
    await waitFor(() => expect(screen.getByTestId('job-state')).toHaveTextContent('running'))
    act(() =>
      fake.emit('job.finished', {
        jobId: 'j1',
        profile: 'default',
        state: 'succeeded',
        finishedAt: '2026-01-01T00:01:00Z',
        exitCode: 0,
      }),
    )
    await waitFor(() => expect(screen.getByTestId('job-state')).toHaveTextContent('succeeded'))
    expect(screen.getByText('exit 0')).toBeInTheDocument()
  })

  it('shows the truncation notice', async () => {
    vi.spyOn(api, 'getJob').mockResolvedValue(job({ truncated: true, droppedLines: 7 }))
    renderAt('/profiles/default/log?job=j1')
    await waitFor(() => expect(pane()).toMatch(/^7 earlier lines were dropped\|/))
  })

  it('shows the empty state on 404 job_not_found', async () => {
    vi.spyOn(api, 'getJob').mockRejectedValue(new ApiError(404, 'job_not_found', 'gone'))
    renderAt('/profiles/default/log?job=gone')
    expect(await screen.findByText(/no longer available/)).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Back to the profile' })).toHaveAttribute(
      'href',
      '/profiles/default',
    )
  })

  it('falls back to the active job of the profile, or an empty state', async () => {
    const getProfile = vi.spyOn(api, 'getProfile')
    const getJob = vi.spyOn(api, 'getJob').mockResolvedValue(job())
    getProfile.mockResolvedValue({
      name: 'default', status: 'running', runtime: 'docker', cpus: 2, memoryBytes: 1,
      diskBytes: 1, colimaVersion: '0.0.0', source: 'both', activeJobId: 'j1',
    })
    const first = renderAt('/profiles/default/log')
    await waitFor(() => expect(getJob).toHaveBeenCalledWith('j1', expect.anything()))
    first.unmount()

    qc.clear()
    getProfile.mockResolvedValue({
      name: 'default', status: 'stopped', runtime: 'docker', cpus: 2, memoryBytes: 1,
      diskBytes: 1, colimaVersion: '0.0.0', source: 'both', activeJobId: null,
    })
    renderAt('/profiles/default/log')
    expect(await screen.findByText(/No operation to show/)).toBeInTheDocument()
  })

  it('cancels a running job and ignores a 409', async () => {
    vi.spyOn(api, 'getJob').mockResolvedValue(job())
    const cancel = vi
      .spyOn(api, 'cancelJob')
      .mockRejectedValueOnce(new ApiError(409, 'job_not_running', 'done'))
      .mockResolvedValueOnce(job({ state: 'cancelled', exitCode: null }))
    renderAt('/profiles/default/log?job=j1')
    const btn = await screen.findByRole('button', { name: 'Cancel' })
    await userEvent.click(btn)
    await waitFor(() => expect(cancel).toHaveBeenCalledTimes(1))
    expect(screen.queryByRole('alert')).toBeNull()
    await userEvent.click(await screen.findByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(cancel).toHaveBeenCalledTimes(2))
    await waitFor(() => expect(screen.getByTestId('job-state')).toHaveTextContent('cancelled'))
  })
})
