import { useQuery } from '@tanstack/react-query'
import { api } from './endpoints'
import { queryKeys } from './keys'

export const NETWORKS_REFETCH_MS = 30_000

/** Networks of a profile, refetched every 30 s while the page is visible and on focus. Idle while `enabled` is false. */
export function useNetworks(profile: string, enabled: boolean) {
  return useQuery({
    queryKey: queryKeys.networks(profile),
    queryFn: ({ signal }) => api.getNetworks(profile, signal),
    enabled,
    refetchInterval: NETWORKS_REFETCH_MS,
    refetchOnWindowFocus: true,
    retry: false,
  })
}
