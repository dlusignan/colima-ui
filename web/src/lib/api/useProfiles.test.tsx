import { renderHook, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { api } from './endpoints'
import { BUSY_REFETCH_MS, IDLE_REFETCH_MS, profilesRefetchInterval, useProfiles } from './useProfiles'
import { profileFixture } from '@/test/fixtures'
import { createTestClient, wrapperFor } from '@/test/queryWrapper'

afterEach(() => vi.restoreAllMocks())

describe('profilesRefetchInterval', () => {
  it('is slow when idle and fast while any profile has a job', () => {
    expect(profilesRefetchInterval(undefined)).toBe(IDLE_REFETCH_MS)
    expect(profilesRefetchInterval({ profiles: [profileFixture()] })).toBe(IDLE_REFETCH_MS)
    expect(
      profilesRefetchInterval({
        profiles: [profileFixture(), profileFixture({ name: 'b', activeJobId: 'j_1' })],
      }),
    ).toBe(BUSY_REFETCH_MS)
  })
})

describe('useProfiles', () => {
  it('loads the list', async () => {
    vi.spyOn(api, 'listProfiles').mockResolvedValue({ profiles: [profileFixture()] })
    const { result } = renderHook(() => useProfiles(), {
      wrapper: wrapperFor(createTestClient()),
    })
    await waitFor(() => expect(result.current.data?.profiles).toHaveLength(1))
  })

  it('polls on the idle interval', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      const spy = vi.spyOn(api, 'listProfiles').mockResolvedValue({ profiles: [] })
      renderHook(() => useProfiles(), { wrapper: wrapperFor(createTestClient()) })
      await waitFor(() => expect(spy).toHaveBeenCalledTimes(1))
      await vi.advanceTimersByTimeAsync(IDLE_REFETCH_MS + 100)
      expect(spy.mock.calls.length).toBeGreaterThanOrEqual(2)
    } finally {
      vi.useRealTimers()
    }
  })
})
