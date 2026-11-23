import { act, renderHook, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/lib/api/client'
import { api } from '@/lib/api/endpoints'
import { queryKeys } from '@/lib/api/keys'
import { createTestClient, wrapperFor } from '@/test/queryWrapper'
import { useProfileActionRunner } from './useProfileActionRunner'

afterEach(() => vi.restoreAllMocks())

function setup() {
  const client = createTestClient()
  const invalidate = vi.spyOn(client, 'invalidateQueries')
  const adopt = vi.fn()
  const onError = vi.fn()
  const hook = renderHook(() => useProfileActionRunner('dev', { adopt }, onError), {
    wrapper: wrapperFor(client),
  })
  return { adopt, onError, invalidate, ...hook }
}

describe('useProfileActionRunner', () => {
  it('adopts the job of a successful action', async () => {
    const stop = vi.spyOn(api, 'stopProfile').mockResolvedValue({ jobId: 'j_1' })
    const { result, adopt, onError } = setup()
    act(() => result.current.run('stop'))
    await waitFor(() => expect(adopt).toHaveBeenCalledWith('dev', 'j_1'))
    expect(stop).toHaveBeenCalledWith('dev')
    expect(onError).not.toHaveBeenCalled()
  })

  it('profile_busy adopts the running job, refreshes and reports', async () => {
    vi.spyOn(api, 'startProfile').mockRejectedValue(
      new ApiError(409, 'profile_busy', 'x', { jobId: 'j_9' }),
    )
    const { result, adopt, onError, invalidate } = setup()
    act(() => result.current.run('start'))
    await waitFor(() => expect(onError).toHaveBeenCalledWith('Another job is already running on dev.'))
    expect(adopt).toHaveBeenCalledWith('dev', 'j_9')
    expect(invalidate).toHaveBeenCalledWith({ queryKey: queryKeys.profiles })
  })

  it('a 401 reports nothing', async () => {
    vi.spyOn(api, 'startProfile').mockRejectedValue(new ApiError(401, 'unauthorized', 'x'))
    const { result, onError, adopt } = setup()
    act(() => result.current.run('start'))
    await waitFor(() => expect(result.current.pending).toBe(false))
    expect(onError).not.toHaveBeenCalled()
    expect(adopt).not.toHaveBeenCalled()
  })
})
