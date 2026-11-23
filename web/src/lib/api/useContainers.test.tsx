import { QueryClient } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { wrapperFor } from '@/test/queryWrapper'
import { ApiError } from './client'
import { api } from './endpoints'
import { useContainers } from './useContainers'

afterEach(() => vi.restoreAllMocks())

it('surfaces an error at once, without retrying', async () => {
  const get = vi.spyOn(api, 'getContainers').mockRejectedValue(new ApiError(502, 'docker_unavailable', 'x'))
  // A client with the library default retry: the hook must override it.
  const client = new QueryClient()
  const { result } = renderHook(() => useContainers('dev', true), { wrapper: wrapperFor(client) })
  await waitFor(() => expect(result.current.isError).toBe(true), { timeout: 500 })
  expect(get).toHaveBeenCalledTimes(1)
})
