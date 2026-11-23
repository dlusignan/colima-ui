import { useQuery } from '@tanstack/react-query'
import { api } from './endpoints'
import { queryKeys } from './keys'

/** Latest action log entries of one profile. */
export function useActions(name: string, limit = 20) {
  return useQuery({
    queryKey: queryKeys.actions(name, limit),
    queryFn: ({ signal }) => api.getActions(name, limit, signal),
    retry: false,
  })
}
