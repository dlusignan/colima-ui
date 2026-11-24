import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api/endpoints'
import { queryKeys } from '@/lib/api/keys'
import { useVolumes } from '@/lib/api/useVolumes'
import { formatBytes, formatRelativeTime } from '@/lib/format'
import type { DockerVolume, Profile } from '@/lib/api/types'
import type { DockerAction } from './ActionButton'
import { CreateVolumeDialog } from './CreateVolumeDialog'
import { RowActions } from './RowActions'
import { SectionError, noteClass } from './SectionState'
import { SectionToolbar } from './SectionToolbar'
import { useDockerMutation } from './useDockerMutation'
import { VolumeInspectDialog } from './VolumeInspectDialog'
import { plural, pruneTitle } from './volumeText'

function removeReason(v: DockerVolume): string | undefined {
  if (v.containers === null) return 'Usage unknown'
  if (v.containers > 0) return `In use by ${plural(v.containers, 'container', 'containers')}`
  return undefined
}

/** Volumes of a running VM. Nothing is requested while the VM is not running. */
export function VolumesSection({ profile }: { profile: Profile }) {
  const running = profile.status === 'running'
  const query = useVolumes(profile.name, running)
  const queryClient = useQueryClient()
  const list = query.data?.volumes
  const [creating, setCreating] = useState(false)
  const [inspecting, setInspecting] = useState<string | null>(null)
  const [removing, setRemoving] = useState<string | null>(null)

  const remove = useDockerMutation({
    profile: profile.name,
    section: 'volumes',
    action: 'volume-remove',
    mutationFn: (name: string) => api.removeVolume(profile.name, name),
    successTitle: (_d, name) => `Removed volume ${name}`,
    errorTitle: (name) => `Could not remove volume ${name}`,
  })
  const prune = useDockerMutation({
    profile: profile.name,
    section: 'volumes',
    action: 'volume-prune',
    mutationFn: () => api.pruneVolumes(profile.name),
    successTitle: (result) => pruneTitle(result),
    errorTitle: () => 'Could not prune volumes',
  })

  // The loaded list only: the server removes whatever is unused when the call runs.
  const unused = (list ?? []).filter((v) => v.containers === 0)
  const unusedKnownBytes = unused.reduce((sum, v) => sum + (v.sizeBytes ?? 0), 0)
  const someSizeUnknown = unused.some((v) => v.sizeBytes === null)

  const toolbarActions: DockerAction[] = [
    { id: 'create', label: 'Create volume', onSelect: () => setCreating(true) },
    {
      id: 'prune',
      label: 'Prune unused',
      onSelect: () => prune.run(),
      pending: prune.isPending,
      disabledReason: !list ? 'Volumes are not loaded' : unused.length === 0 ? 'No unused volumes' : undefined,
      confirm: {
        title: `Prune ${plural(unused.length, 'unused volume', 'unused volumes')}?`,
        description:
          `This deletes ALL volumes that no container uses, including volumes you created and named yourself. ` +
          `The volumes below are unused in the list on screen, ${someSizeUnknown ? 'at least ' : ''}${formatBytes(unusedKnownBytes)} in total` +
          `${someSizeUnknown ? ' (some sizes are unknown)' : ''}. ` +
          `The server removes every volume that is unused when you confirm, which can include volumes created since this list loaded. ` +
          `The data is deleted and cannot be recovered.`,
        confirmLabel: 'Prune volumes',
        destructive: true,
        items: unused.map((v) => `${v.name} (${v.sizeBytes === null ? 'size unknown' : formatBytes(v.sizeBytes)})`),
        requireText: 'prune',
      },
    },
  ]

  let body
  if (!running) {
    body = <p className={noteClass}>Start the profile to see its volumes.</p>
  } else if (query.isPending) {
    body = <p className={noteClass}>Loading volumes…</p>
  } else if (query.isError && !list) {
    body = (
      <SectionError
        error={query.error}
        fallback="Volumes could not be loaded."
        onRetry={() => void queryClient.invalidateQueries({ queryKey: queryKeys.volumes(profile.name) })}
      />
    )
  } else if (!list || list.length === 0) {
    body = <p className={noteClass}>No volumes.</p>
  } else {
    body = (
      <div className="overflow-x-auto rounded-lg border border-border" tabIndex={0} role="region" aria-label="Volumes table">
        <table className="w-full text-sm">
          <caption className="sr-only">Volumes in {profile.name}</caption>
          <thead>
            <tr className="border-b border-border text-left text-xs text-muted-foreground">
              {['Name', 'Driver', 'Size', 'Created', 'Used by', 'Actions'].map((c) => (
                <th key={c} scope="col" className="px-3 py-2 font-medium">
                  {c}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {list.map((v) => {
              const created = new Date(v.createdAt)
              return (
                <tr key={v.name} className="border-b border-border last:border-0 align-top">
                  <th scope="row" className="px-3 py-2 text-left font-mono font-medium break-all">
                    {v.name}
                  </th>
                  <td className="px-3 py-2">{v.driver}</td>
                  <td className="px-3 py-2 font-mono whitespace-nowrap">
                    {v.sizeBytes === null ? '–' : formatBytes(v.sizeBytes)}
                  </td>
                  <td className="px-3 py-2 whitespace-nowrap">
                    <span title={Number.isNaN(created.getTime()) ? undefined : created.toLocaleString()}>
                      {formatRelativeTime(v.createdAt)}
                    </span>
                  </td>
                  <td className="px-3 py-2 whitespace-nowrap">
                    {v.containers === null ? '–' : v.containers === 0 ? 'Unused' : v.containers}
                  </td>
                  <td className="px-3 py-2">
                    <RowActions
                      object={v.name}
                      actions={[
                        { id: 'inspect', label: 'Inspect', onSelect: () => setInspecting(v.name) },
                        {
                          id: 'remove',
                          label: 'Remove',
                          onSelect: () => {
                            setRemoving(v.name)
                            remove.run(v.name)
                          },
                          pending: remove.isPending && removing === v.name,
                          disabledReason: removeReason(v),
                          confirm: {
                            title: `Remove volume ${v.name}?`,
                            description:
                              'The volume and all data in it are deleted. This cannot be recovered. Containers that use it block the removal.',
                            confirmLabel: 'Remove volume',
                            destructive: true,
                            items: [v.name],
                            requireText: v.name,
                          },
                        },
                      ]}
                    />
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
    )
  }

  return (
    <section aria-labelledby="volumes-heading" className="flex flex-col gap-2">
      <h3 id="volumes-heading" className="text-base font-semibold">
        Volumes
      </h3>
      <SectionToolbar
        label="Volume actions"
        actions={toolbarActions}
        disabledReason={running ? undefined : 'Start the profile to use these actions.'}
      />
      {body}
      <CreateVolumeDialog profile={profile.name} open={creating} onOpenChange={setCreating} />
      <VolumeInspectDialog profile={profile.name} volume={inspecting} onClose={() => setInspecting(null)} />
    </section>
  )
}
