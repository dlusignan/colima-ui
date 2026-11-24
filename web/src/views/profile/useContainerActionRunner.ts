import { useRef, useState } from 'react'
import { api } from '@/lib/api/endpoints'
import { useDockerMutation } from '@/views/docker/useDockerMutation'
import {
  containerActionTitle,
  describeContainerActionError,
  type ContainerActionName,
} from './containerActionText'

export interface ContainerActionVars {
  id: string
  name: string
  action: ContainerActionName
}

/**
 * One mutation runs runs at a time for the whole table,
 * at a time for the whole table, and a second confirm while it is in flight is ignored.
 */
export function useContainerActionRunner(profile: string) {
  const inFlight = useRef(false)
  const [activeId, setActiveId] = useState<string | null>(null)
  const mutation = useDockerMutation<ContainerActionVars, { changed: boolean }>({
    profile,
    section: 'containers',
    action: 'container-action',
    mutationFn: ({ id, action }) => api.containerAction(profile, id, action),
    successTitle: ({ changed }, { action, name }) => containerActionTitle(action, name, changed),
    errorTitle: ({ action, name }) => `Could not ${action} ${name}`,
    describeError: describeContainerActionError,
    onSettled: () => {
      inFlight.current = false
      setActiveId(null)
    },
  })
  return {
    pending: mutation.isPending,
    activeId,
    run: (vars: ContainerActionVars) => {
      if (inFlight.current) return
      inFlight.current = true
      setActiveId(vars.id)
      mutation.run(vars)
    },
  }
}

export type ContainerActionRunner = ReturnType<typeof useContainerActionRunner>
