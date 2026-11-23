import { describe, expect, it } from 'vitest'
import { ApiError } from '@/lib/api/client'
import { createQueryClient, shouldRetry } from './queryClient'

describe('query client defaults', () => {
  it('uses a 2 second staleTime and never retries mutations', () => {
    const d = createQueryClient().getDefaultOptions()
    expect(d.queries?.staleTime).toBe(2000)
    expect(d.mutations?.retry).toBe(false)
  })

  it('does not retry 4xx errors', () => {
    for (const status of [400, 401, 404, 409, 422, 499]) {
      expect(shouldRetry(0, new ApiError(status, 'x', 'x'))).toBe(false)
    }
  })

  it('retries other errors at most twice', () => {
    for (const err of [new ApiError(502, 'x', 'x'), new ApiError(0, 'network_error', 'x'), new Error('x')]) {
      expect(shouldRetry(0, err)).toBe(true)
      expect(shouldRetry(1, err)).toBe(true)
      expect(shouldRetry(2, err)).toBe(false)
    }
  })

  it('wires shouldRetry as the query default', () => {
    expect(createQueryClient().getDefaultOptions().queries?.retry).toBe(shouldRetry)
  })
})
