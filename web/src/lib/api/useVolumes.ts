import { useQuery } from '@tanstack/react-query'
import { api } from './endpoints'
import { queryKeys } from './keys'

export const VOLUMES_REFETCH_MS = 30_000

/** Volumes of a profile, refetched every 30 s while the page is visible and on focus. Idle while `enabled` is false. */
export function useVolumes(profile: string, enabled: boolean) {
  return useQuery({
    queryKey: queryKeys.volumes(profile),
    queryFn: ({ signal }) => api.getVolumes(profile, signal),
    enabled,
    refetchInterval: VOLUMES_REFETCH_MS,
    refetchOnWindowFocus: true,
    retry: false,
  })
}
