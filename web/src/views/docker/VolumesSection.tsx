import { useQueryClient } from '@tanstack/react-query'
import { queryKeys } from '@/lib/api/keys'
import { useVolumes } from '@/lib/api/useVolumes'
import { formatBytes, formatRelativeTime } from '@/lib/format'
import type { Profile } from '@/lib/api/types'
import { SectionError, noteClass } from './SectionState'

/** Volumes of a running VM. Nothing is requested while the VM is not running. */
export function VolumesSection({ profile }: { profile: Profile }) {
  const running = profile.status === 'running'
  const query = useVolumes(profile.name, running)
  const queryClient = useQueryClient()
  const list = query.data?.volumes

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
              {['Name', 'Driver', 'Size', 'Created', 'Used by'].map((c) => (
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
      {body}
    </section>
  )
}
