import { useQuery } from '@tanstack/react-query'
import { api } from './endpoints'
import { queryKeys } from './keys'

/**
 * Whether the profile has Kubernetes enabled, read live from Colima. Only asked
 * while `enabled` (the profile is running: a stopped one answers 409); an error
 * counts as "not known", so the Kubernetes group stays hidden.
 */
export function useKubernetes(profile: string, enabled: boolean) {
  return useQuery({
    queryKey: queryKeys.kubernetes(profile),
    queryFn: ({ signal }) => api.getKubernetes(profile, signal),
    enabled,
    retry: false,
    refetchOnWindowFocus: false,
  })
}
