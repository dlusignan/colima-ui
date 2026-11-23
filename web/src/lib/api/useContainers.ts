import { useQuery } from '@tanstack/react-query'
import { api } from './endpoints'
import { queryKeys } from './keys'

export const CONTAINERS_REFETCH_MS = 5_000

/** Container usage, polled while `enabled` (the profile is running). */
export function useContainers(profile: string, enabled: boolean) {
  return useQuery({
    queryKey: queryKeys.containers(profile),
    queryFn: ({ signal }) => api.getContainers(profile, signal),
    enabled,
    refetchInterval: CONTAINERS_REFETCH_MS,
    retry: false,
  })
}
