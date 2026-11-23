import { useQuery } from '@tanstack/react-query'
import { api } from './endpoints'
import { queryKeys } from './keys'

export const ENGINE_REFETCH_MS = 30_000

/** Engine facts and disk usage of a profile, refetched every 30 s while visible and on focus. Idle while `enabled` is false. */
export function useEngine(profile: string, enabled: boolean) {
  return useQuery({
    queryKey: queryKeys.engine(profile),
    queryFn: ({ signal }) => api.getEngine(profile, signal),
    enabled,
    refetchInterval: ENGINE_REFETCH_MS,
    refetchOnWindowFocus: true,
    retry: false,
  })
}
