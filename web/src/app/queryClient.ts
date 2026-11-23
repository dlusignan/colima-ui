import { QueryClient } from '@tanstack/react-query'
import { ApiError } from '@/lib/api/client'

const MAX_RETRIES = 2

/** No retry on 4xx; at most two retries for everything else. */
export function shouldRetry(failureCount: number, error: unknown): boolean {
  if (error instanceof ApiError && error.status >= 400 && error.status < 500) {
    return false
  }
  return failureCount < MAX_RETRIES
}

export function createQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      // State is live; the cache is never treated as truth for long.
      queries: { staleTime: 2_000, retry: shouldRetry },
      mutations: { retry: false },
    },
  })
}

export const queryClient = createQueryClient()
