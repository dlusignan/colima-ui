import { useQueryClient } from '@tanstack/react-query'
import { queryKeys } from '@/lib/api/keys'
import { useNetworks } from '@/lib/api/useNetworks'
import type { Profile } from '@/lib/api/types'
import { SectionError, noteClass } from './SectionState'

/** Networks of a running VM. Nothing is requested while the VM is not running. */
export function NetworksSection({ profile }: { profile: Profile }) {
  const running = profile.status === 'running'
  const query = useNetworks(profile.name, running)
  const queryClient = useQueryClient()
  const list = query.data?.networks

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
                {['Name', 'Driver', 'Scope', 'Internal', 'Containers'].map((c) => (
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
      {body}
    </section>
  )
}
