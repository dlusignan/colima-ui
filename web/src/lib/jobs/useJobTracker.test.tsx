import { act, renderHook, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/lib/api/client'
import { api } from '@/lib/api/endpoints'
import { queryKeys } from '@/lib/api/keys'
import type { Profile } from '@/lib/api/types'
import { jobFixture, profileFixture } from '@/test/fixtures'
import { createTestClient, wrapperFor } from '@/test/queryWrapper'
import { useJobTracker } from './useJobTracker'

afterEach(() => vi.restoreAllMocks())

describe('useJobTracker', () => {
  it('follows the active job of a profile', async () => {
    vi.spyOn(api, 'getJob').mockResolvedValue(jobFixture({ id: 'j_1', action: 'stop' }))
    const profiles = [profileFixture({ activeJobId: 'j_1' }), profileFixture({ name: 'synthetic-b' })]
    const { result } = renderHook(() => useJobTracker(profiles), {
      wrapper: wrapperFor(createTestClient()),
    })
    await waitFor(() => expect(result.current.jobs.get('synthetic-a')?.state).toBe('running'))
    expect(result.current.jobs.get('synthetic-a')).toMatchObject({ jobId: 'j_1', action: 'stop' })
    expect(result.current.jobs.has('synthetic-b')).toBe(false)
  })

  it('holds a failed outcome after the list drops the job, refetches the list once, and dismisses', async () => {
    const get = vi.spyOn(api, 'getJob').mockResolvedValue(jobFixture({ id: 'j_1' }))
    const client = createTestClient()
    const invalidate = vi.spyOn(client, 'invalidateQueries')
    const running: Profile[] = [profileFixture({ activeJobId: 'j_1' })]
    const { result, rerender } = renderHook(({ p }) => useJobTracker(p), {
      wrapper: wrapperFor(client),
      initialProps: { p: running },
    })
    await waitFor(() => expect(result.current.jobs.get('synthetic-a')?.state).toBe('running'))

    get.mockResolvedValue(
      jobFixture({ id: 'j_1', state: 'failed', exitCode: 1, finishedAt: '2026-01-01T00:01:00Z' }),
    )
    await act(() => client.invalidateQueries({ queryKey: queryKeys.job('j_1') }))
    await waitFor(() => expect(result.current.jobs.get('synthetic-a')?.state).toBe('failed'))
    expect(
      invalidate.mock.calls.filter(([f]) => f?.queryKey === queryKeys.profiles),
    ).toHaveLength(1)

    // The list no longer carries activeJobId; the outcome stays.
    rerender({ p: [profileFixture({ activeJobId: null })] })
    await waitFor(() =>
      expect(result.current.jobs.get('synthetic-a')).toMatchObject({ state: 'failed', exitCode: 1 }),
    )

    act(() => result.current.dismiss('synthetic-a'))
    expect(result.current.jobs.has('synthetic-a')).toBe(false)
  })

  it('adopts a job id from a profile_busy answer', async () => {
    vi.spyOn(api, 'getJob').mockResolvedValue(jobFixture({ id: 'j_7', action: 'restart' }))
    const { result } = renderHook(() => useJobTracker([profileFixture()]), {
      wrapper: wrapperFor(createTestClient()),
    })
    expect(result.current.jobs.size).toBe(0)
    act(() => result.current.adopt('synthetic-a', 'j_7'))
    await waitFor(() => expect(result.current.jobs.get('synthetic-a')?.jobId).toBe('j_7'))
  })

  it('retries a job_not_found once after a delay, and nothing else', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      const get = vi
        .spyOn(api, 'getJob')
        .mockRejectedValueOnce(new ApiError(404, 'job_not_found', 'x'))
        .mockResolvedValue(jobFixture({ id: 'j_1' }))
      const { result } = renderHook(() => useJobTracker([profileFixture({ activeJobId: 'j_1' })]), {
        wrapper: wrapperFor(createTestClient()),
      })
      await waitFor(() => expect(get).toHaveBeenCalledTimes(1))
      await vi.advanceTimersByTimeAsync(600)
      await waitFor(() => expect(result.current.jobs.get('synthetic-a')?.state).toBe('running'))
      expect(get).toHaveBeenCalledTimes(2)
    } finally {
      vi.useRealTimers()
    }
  })

  it('does not retry other errors', async () => {
    const get = vi.spyOn(api, 'getJob').mockRejectedValue(new ApiError(500, 'boom', 'x'))
    renderHook(() => useJobTracker([profileFixture({ activeJobId: 'j_1' })]), {
      wrapper: wrapperFor(createTestClient()),
    })
    await waitFor(() => expect(get).toHaveBeenCalledTimes(1))
    await new Promise((r) => setTimeout(r, 700))
    expect(get).toHaveBeenCalledTimes(1)
  })
})
