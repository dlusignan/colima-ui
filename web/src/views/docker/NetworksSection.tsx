import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api/endpoints'
import { queryKeys } from '@/lib/api/keys'
import { useNetworks } from '@/lib/api/useNetworks'
import type { Profile } from '@/lib/api/types'
import type { DockerAction } from './ActionButton'
import { CreateNetworkDialog } from './CreateNetworkDialog'
import { NetworkInspectDialog } from './NetworkInspectDialog'
import { isBuiltInNetwork } from './networkRules'
import { networkPruneTitle, networkRemoveReason } from './networkText'
import { RowActions } from './RowActions'
import { SectionError, noteClass } from './SectionState'
import { SectionToolbar } from './SectionToolbar'
import { useDockerMutation } from './useDockerMutation'
import { plural } from './volumeText'

/** Networks of a running VM. Nothing is requested while the VM is not running. */
export function NetworksSection({ profile }: { profile: Profile }) {
  const running = profile.status === 'running'
  const query = useNetworks(profile.name, running)
  const queryClient = useQueryClient()
  const list = query.data?.networks
  const [creating, setCreating] = useState(false)
  const [inspecting, setInspecting] = useState<string | null>(null)
  const [removing, setRemoving] = useState<string | null>(null)

  const remove = useDockerMutation({
    profile: profile.name,
    section: 'networks',
    action: 'network-remove',
    mutationFn: (name: string) => api.removeNetwork(profile.name, name),
    successTitle: (_d, name) => `Removed network ${name}`,
    errorTitle: (name) => `Could not remove network ${name}`,
  })
  const prune = useDockerMutation({
    profile: profile.name,
    section: 'networks',
    action: 'network-prune',
    mutationFn: () => api.pruneNetworks(profile.name),
    successTitle: (result) => networkPruneTitle(result),
    errorTitle: () => 'Could not prune networks',
  })

  // The loaded list only. Docker's own rule decides on the server and may also remove networks
  // that only stopped containers use, so this list is a lower bound.
  const unused = (list ?? []).filter((n) => n.containers === 0 && !isBuiltInNetwork(n.name))

  const toolbarActions: DockerAction[] = [
    { id: 'create', label: 'Create network', onSelect: () => setCreating(true) },
    {
      id: 'prune',
      label: 'Prune unused',
      onSelect: () => prune.run(),
      pending: prune.isPending,
      disabledReason: !list ? 'Networks are not loaded' : unused.length === 0 ? 'No unused networks' : undefined,
      confirm: {
        title: `Prune ${plural(unused.length, 'unused network', 'unused networks')}?`,
        description:
          `This removes networks that no container uses. The networks below have no container in the list on screen. ` +
          `Docker decides what counts as unused when you confirm, so it can also remove networks that only stopped containers use, ` +
          `and networks created since this list loaded: the list is a minimum, not the full set. ` +
          `Containers lose the network they were configured for, and this cannot be undone. Networks built into Docker are never removed.`,
        confirmLabel: 'Prune networks',
        destructive: true,
        items: unused.map((n) => n.name),
      },
    },
  ]

  let body
  if (!running) {
    body = <p className={noteClass}>Start the profile to see its networks.</p>
  } else if (query.isPending) {
    body = <p className={noteClass}>Loading networks…</p>
  } else if (query.isError && !list) {
    body = (
      <SectionError
        error={query.error}
        fallback="Networks could not be loaded."
        onRetry={() => void queryClient.invalidateQueries({ queryKey: queryKeys.networks(profile.name) })}
      />
    )
  } else if (!list || list.length === 0) {
    body = <p className={noteClass}>No networks.</p>
  } else {
    body = (
      <>
        <div className="overflow-x-auto rounded-lg border border-border" tabIndex={0} role="region" aria-label="Networks table">
          <table className="w-full text-sm">
            <caption className="sr-only">Networks in {profile.name}</caption>
            <thead>
              <tr className="border-b border-border text-left text-xs text-muted-foreground">
                {['Name', 'Driver', 'Scope', 'Internal', 'Containers', 'Actions'].map((c) => (
                  <th key={c} scope="col" className="px-3 py-2 font-medium">
                    {c}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {list.map((n) => (
                <tr key={n.id} className="border-b border-border last:border-0 align-top">
                  <th scope="row" className="px-3 py-2 text-left font-mono font-medium break-all">
                    {n.name}
                  </th>
                  <td className="px-3 py-2">{n.driver}</td>
                  <td className="px-3 py-2">{n.scope}</td>
                  <td className="px-3 py-2">{n.internal ? 'Yes' : 'No'}</td>
                  <td className="px-3 py-2 whitespace-nowrap">{n.containers}</td>
                  <td className="px-3 py-2">
                    <RowActions
                      object={n.name}
                      actions={[
                        { id: 'inspect', label: 'Inspect', onSelect: () => setInspecting(n.name) },
                        {
                          id: 'remove',
                          label: 'Remove',
                          onSelect: () => {
                            setRemoving(n.name)
                            remove.run(n.name)
                          },
                          pending: remove.isPending && removing === n.name,
                          disabledReason: networkRemoveReason(n),
                          confirm: {
                            title: `Remove network ${n.name}?`,
                            description:
                              'The network is removed. No data is lost, but containers can no longer join it. Docker refuses the removal if a container is still attached.',
                            confirmLabel: 'Remove network',
                            destructive: true,
                            items: [n.name],
                          },
                        },
                      ]}
                    />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <p className="text-xs text-muted-foreground">The container count includes stopped containers.</p>
      </>
    )
  }

  return (
    <section aria-labelledby="networks-heading" className="flex flex-col gap-2">
      <h3 id="networks-heading" className="text-base font-semibold">
        Networks
      </h3>
      <SectionToolbar
        label="Network actions"
        actions={toolbarActions}
        disabledReason={running ? undefined : 'Start the profile to use these actions.'}
      />
      {body}
      <CreateNetworkDialog profile={profile.name} open={creating} onOpenChange={setCreating} />
      <NetworkInspectDialog profile={profile.name} network={inspecting} onClose={() => setInspecting(null)} />
    </section>
  )
}
