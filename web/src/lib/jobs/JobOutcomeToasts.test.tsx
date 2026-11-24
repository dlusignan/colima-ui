import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '@/lib/api/endpoints'
import type { JobFinishedEvent } from '@/lib/api/types'
import { EventStreamTestProvider } from '@/lib/events/EventStreamTestProvider'
import { createFakeEventStream } from '@/lib/events/testing'
import { Toaster } from '@/lib/toast/Toaster'
import { ToastProvider } from '@/lib/toast/ToastProvider'
import { expectNoA11yViolations } from '@/test/axe'
import { jobFixture } from '@/test/fixtures'
import { createTestClient } from '@/test/queryWrapper'
import { JobOutcomeToasts } from './JobOutcomeToasts'

function Where() {
  const l = useLocation()
  return <p data-testid="where">{l.pathname + l.search}</p>
}

function setup() {
  const stream = createFakeEventStream()
  const view = render(
    <QueryClientProvider client={createTestClient()}>
      <EventStreamTestProvider client={stream}>
        <MemoryRouter initialEntries={['/elsewhere']}>
          <ToastProvider>
            <JobOutcomeToasts />
            <Routes>
              <Route path="*" element={<Where />} />
            </Routes>
            <Toaster />
          </ToastProvider>
        </MemoryRouter>
      </EventStreamTestProvider>
    </QueryClientProvider>,
  )
  return { stream, user: userEvent.setup({ advanceTimers: vi.advanceTimersByTime }), ...view }
}

const finished = (over: Partial<JobFinishedEvent> = {}): JobFinishedEvent => ({
  jobId: 'j_1',
  profile: 'default',
  state: 'succeeded',
  finishedAt: '2026-01-01T00:01:00Z',
  exitCode: 0,
  ...over,
})

const emit = async (stream: ReturnType<typeof setup>['stream'], e: JobFinishedEvent) => {
  await act(async () => {
    stream.emit('job.finished', e)
    await vi.advanceTimersByTimeAsync(0)
  })
}

beforeEach(() => vi.useFakeTimers({ shouldAdvanceTime: true }))
afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
})

describe('JobOutcomeToasts', () => {
  it('shows one status toast per job, announced once, and it auto-dismisses after 5 s', async () => {
    const get = vi.spyOn(api, 'getJob').mockResolvedValue(jobFixture({ id: 'j_1', action: 'stop' }))
    const { stream, container } = setup()
    await emit(stream, finished())
    const toast = await screen.findByRole('status')
    expect(toast).toHaveTextContent('default: stop succeeded')
    await expectNoA11yViolations(container)
    // A replayed or duplicate event does not stack a second toast.
    await emit(stream, finished())
    await emit(stream, finished())
    expect(screen.getAllByRole('status')).toHaveLength(1)
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(get).toHaveBeenCalledTimes(1)
    await act(() => vi.advanceTimersByTimeAsync(5_100))
    await waitFor(() => expect(screen.queryByRole('status')).not.toBeInTheDocument())
  })

  it('names an image pull in plain words', async () => {
    vi.spyOn(api, 'getJob').mockResolvedValue(jobFixture({ id: 'j_p', action: 'image-pull' }))
    const { stream } = setup()
    await emit(stream, finished({ jobId: 'j_p', state: 'cancelled', exitCode: null }))
    expect(await screen.findByRole('status')).toHaveTextContent('default: image pull cancelled')
  })

  it('names a global cache clear without a profile prefix or a log link', async () => {
    vi.spyOn(api, 'getJob').mockResolvedValue(jobFixture({ id: 'j_c', profile: '', action: 'cache-prune' }))
    const { stream } = setup()
    await emit(stream, finished({ jobId: 'j_c', profile: '', state: 'succeeded' }))
    const toast = await screen.findByRole('status')
    expect(toast).toHaveTextContent('download cache clear succeeded')
    expect(toast).not.toHaveTextContent(/^:/)
    expect(within(toast).queryByRole('link', { name: 'View log' })).not.toBeInTheDocument()
  })

  it('keeps a failed job as an error toast with the exit code', async () => {
    vi.spyOn(api, 'getJob').mockResolvedValue(jobFixture({ id: 'j_2', action: 'start' }))
    const { stream } = setup()
    await emit(stream, finished({ jobId: 'j_2', state: 'failed', exitCode: 1 }))
    expect(await screen.findByRole('alert')).toHaveTextContent('default: start failed (exit 1)')
    await act(() => vi.advanceTimersByTimeAsync(30_000))
    expect(screen.getByRole('alert')).toBeInTheDocument()
  })

  it('shows a cancelled job as an info toast', async () => {
    vi.spyOn(api, 'getJob').mockResolvedValue(jobFixture({ id: 'j_3', action: 'restart' }))
    const { stream } = setup()
    await emit(stream, finished({ jobId: 'j_3', state: 'cancelled', exitCode: null }))
    expect(await screen.findByRole('status')).toHaveTextContent('default: restart cancelled')
  })

  it('names a create job on the new profile', async () => {
    vi.spyOn(api, 'getJob').mockResolvedValue(jobFixture({ id: 'j_4', profile: 'new', action: 'create' }))
    const { stream } = setup()
    await emit(stream, finished({ jobId: 'j_4', profile: 'new' }))
    expect(await screen.findByRole('status')).toHaveTextContent('new: create succeeded')
  })

  it('View log is a keyboard operable link that opens that job log, wherever the user is', async () => {
    vi.spyOn(api, 'getJob').mockResolvedValue(jobFixture({ id: 'j_5', action: 'stop' }))
    const { stream, user } = setup()
    expect(screen.getByTestId('where')).toHaveTextContent('/elsewhere')
    await emit(stream, finished({ jobId: 'j_5' }))
    const link = await within(await screen.findByRole('status')).findByRole('link', { name: 'View log' })
    expect(link).toHaveAttribute('href', '/profiles/default/log?job=j_5')
    link.focus()
    await user.keyboard('{Enter}')
    expect(screen.getByTestId('where')).toHaveTextContent('/profiles/default/log?job=j_5')
    await waitFor(() => expect(screen.queryByRole('status')).not.toBeInTheDocument())
  })

  it('still shows the outcome, without the action, when the job cannot be read', async () => {
    vi.spyOn(api, 'getJob').mockRejectedValue(new Error('boom'))
    const { stream } = setup()
    await emit(stream, finished({ jobId: 'j_6', state: 'failed', exitCode: 2 }))
    expect(await screen.findByRole('alert')).toHaveTextContent('default: job failed (exit 2)')
  })

  it('shows nothing for events before mount', async () => {
    const get = vi.spyOn(api, 'getJob')
    const { stream } = setup()
    await act(() => vi.advanceTimersByTimeAsync(1_000))
    expect(screen.queryByRole('status')).not.toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(get).not.toHaveBeenCalled()
    expect(stream.connectCalls).toBeGreaterThanOrEqual(0)
  })
})
