import { useMutation, useQueryClient } from '@tanstack/react-query'
import { ApiError } from '@/lib/api/client'
import { queryKeys } from '@/lib/api/keys'
import { useToast } from '@/lib/toast/useToast'
import { describeDockerError } from './describeDockerError'

export type DockerSection = 'images' | 'volumes' | 'networks'

export interface DockerMutationOptions<TVars, TData> {
  /** Profile the call belongs to; scopes the invalidated queries and the error key. */
  profile: string
  /** Section whose list is refetched afterwards (the engine query always is). */
  section: DockerSection
  /** The API call. Receives the variables passed to `run`. */
  mutationFn: (vars: TVars) => Promise<TData>
  /** Success toast title. */
  successTitle: (data: TData, vars: TVars) => string
  /** Error toast title and the text used when the error carries nothing readable, e.g. "Could not remove volume". */
  errorTitle: (vars: TVars) => string
  /** Stable name of the action, part of the dedupe key ("volume-remove"). */
  action: string
  /** Runs after a successful call and its toast (close a dialog, for example). */
  onSuccess?: (data: TData, vars: TVars) => void
  /**
   * Sees a failed call before the toast. Return true when the caller shows the
   * error itself (next to a form field): no error toast is then shown. The
   * lists are refetched either way.
   */
  handleError?: (error: unknown, vars: TVars) => boolean
}

/**
 * Runs one Docker manager action with the outcome handling the three sections
 * share: exactly one toast per outcome (an error toast is keyed by profile,
 * action and error code, so repeating the same failure replaces it instead of
 * stacking), invalidation of the section list and the engine query (disk usage
 * changes) whether the call succeeded or not (a 409 usually means the list was
 * stale), and a pending flag the caller uses to disable the triggering control.
 * A rejected token (401) shows no toast. Needs a ToastProvider and QueryClientProvider.
 *
 * @returns `run` starts the call and never rejects; `isPending` is true while it runs.
 */
export function useDockerMutation<TVars = void, TData = unknown>(
  options: DockerMutationOptions<TVars, TData>,
) {
  const { profile, section, action } = options
  const toast = useToast()
  const queryClient = useQueryClient()
  const mutation = useMutation<TData, unknown, TVars>({
    mutationFn: options.mutationFn,
    onSuccess: (data, vars) => {
      toast.success({ title: options.successTitle(data, vars) })
      options.onSuccess?.(data, vars)
    },
    onError: (error, vars) => {
      if (options.handleError?.(error, vars)) return
      const title = options.errorTitle(vars)
      const description = describeDockerError(error, title)
      if (description === '') return
      const code = error instanceof ApiError ? `${error.status}:${error.code}` : 'unknown'
      toast.error({ title, description, key: `docker:${profile}:${action}:${code}` })
    },
    onSettled: () =>
      Promise.all([
        queryClient.invalidateQueries({ queryKey: queryKeys[section](profile) }),
        queryClient.invalidateQueries({ queryKey: queryKeys.engine(profile) }),
      ]),
  })
  return {
    run: (vars: TVars) => mutation.mutate(vars),
    isPending: mutation.isPending,
  }
}
