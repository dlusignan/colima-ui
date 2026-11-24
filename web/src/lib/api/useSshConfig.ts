import { useQuery } from '@tanstack/react-query'
import { api } from './endpoints'
import { queryKeys } from './keys'

/** The profile's SSH config, fetched once while `enabled` (the dialog is open); no polling, nothing kept after close. */
export function useSshConfig(profile: string, enabled: boolean) {
  return useQuery({
    queryKey: queryKeys.sshConfig(profile),
    queryFn: ({ signal }) => api.getSshConfig(profile, signal),
    enabled,
    retry: false,
    gcTime: 0,
    refetchOnWindowFocus: false,
  })
}
