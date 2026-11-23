import { useQuery } from '@tanstack/react-query'
import { api } from './endpoints'
import { queryKeys } from './keys'

/** One snapshot of a container's recent output. Fetched only while `enabled` (the dialog is open); no polling. */
export function useContainerLogs(profile: string, id: string, tail: number, enabled: boolean) {
  return useQuery({
    queryKey: queryKeys.containerLogs(profile, id, tail),
    queryFn: ({ signal }) => api.getContainerLogs(profile, id, tail, signal),
    enabled,
    retry: false,
    gcTime: 0,
  })
}
