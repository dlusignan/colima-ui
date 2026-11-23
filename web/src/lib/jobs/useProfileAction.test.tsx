import { act, renderHook, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { api } from '@/lib/api/endpoints'
import { queryKeys } from '@/lib/api/keys'
import type { ProfileList } from '@/lib/api/types'
import { profileFixture } from '@/test/fixtures'
import { createTestClient, wrapperFor } from '@/test/queryWrapper'
import { useProfileAction } from './useProfileAction'

afterEach(() => vi.restoreAllMocks())

function seed() {
  const client = createTestClient()
  client.setQueryData<ProfileList>(queryKeys.profiles, {
    profiles: [profileFixture(), profileFixture({ name: 'synthetic-b' })],
  })
  return client
}

describe('useProfileAction', () => {
  it.each([
    ['start', 'startProfile'],
    ['stop', 'stopProfile'],
    ['restart', 'restartProfile'],
  ] as const)('%s calls the matching route', async (action, method) => {
    const spy = vi.spyOn(api, method).mockResolvedValue({ jobId: 'j_5' })
    const { result } = renderHook(() => useProfileAction('synthetic-a'), {
      wrapper: wrapperFor(seed()),
    })
    await act(() => result.current.mutateAsync({ action }))
    expect(spy).toHaveBeenCalledWith('synthetic-a')
  })

  it('writes the job id into the cached row at once and leaves the status alone', async () => {
    vi.spyOn(api, 'startProfile').mockResolvedValue({ jobId: 'j_5' })
    const client = seed()
    const refetch = vi.spyOn(client, 'invalidateQueries')
    const { result } = renderHook(() => useProfileAction('synthetic-a'), {
      wrapper: wrapperFor(client),
    })
    await act(() => result.current.mutateAsync({ action: 'start' }))
    const list = client.getQueryData<ProfileList>(queryKeys.profiles)
    expect(list?.profiles[0]).toMatchObject({ activeJobId: 'j_5', status: 'running' })
    expect(list?.profiles[1].activeJobId).toBeNull()
    expect(refetch).toHaveBeenCalledWith({ queryKey: queryKeys.profiles })
  })

  it('is pending between click and answer', async () => {
    let resolve!: (v: { jobId: string }) => void
    vi.spyOn(api, 'stopProfile').mockReturnValue(new Promise((r) => (resolve = r)))
    const { result } = renderHook(() => useProfileAction('synthetic-a'), {
      wrapper: wrapperFor(seed()),
    })
    act(() => result.current.mutate({ action: 'stop' }))
    await waitFor(() => expect(result.current.isPending).toBe(true))
    await act(async () => resolve({ jobId: 'j_1' }))
    await waitFor(() => expect(result.current.isPending).toBe(false))
  })

  it('keeps the cache unchanged on error', async () => {
    vi.spyOn(api, 'startProfile').mockRejectedValue(new Error('no'))
    const client = seed()
    const { result } = renderHook(() => useProfileAction('synthetic-a'), {
      wrapper: wrapperFor(client),
    })
    await act(() => result.current.mutateAsync({ action: 'start' }).catch(() => undefined))
    expect(client.getQueryData<ProfileList>(queryKeys.profiles)?.profiles[0].activeJobId).toBeNull()
  })
})
