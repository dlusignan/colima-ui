import type { Profile } from '@/lib/api/types'
import { useProfileActionRunner } from '@/lib/jobs/useProfileActionRunner'
import type { JobTracker } from '@/lib/jobs/useJobTracker'
import { ProfileRow } from './ProfileRow'

/** Column label and the classes that hide it on narrow screens. */
const COLUMNS = [
  ['Name', ''],
  ['Status', ''],
  ['Runtime', 'hidden md:table-cell'],
  ['CPUs', 'hidden sm:table-cell'],
  ['Memory', 'hidden sm:table-cell'],
  ['Disk', 'hidden lg:table-cell'],
  ['Colima', 'hidden xl:table-cell'],
  ['Source', 'hidden xl:table-cell'],
  ['Warnings', ''],
  ['Actions', ''],
] as const

interface RowProps {
  profile: Profile
  tracker: JobTracker
  unavailable: boolean
  onError: (message: string) => void
}

function Row({ profile, tracker, unavailable, onError }: RowProps) {
  const runner = useProfileActionRunner(profile.name, tracker, onError)
  return (
    <ProfileRow
      profile={profile}
      job={tracker.jobs.get(profile.name)}
      pending={runner.pending}
      unavailable={unavailable}
      onAction={runner.run}
      onDelete={runner.runDelete}
    />
  )
}

interface ProfileTableProps {
  profiles: readonly Profile[]
  tracker: JobTracker
  onError: RowProps['onError']
}

/** Columns hide progressively on narrow screens; the scroll container is the last resort. */
export function ProfileTable({ profiles, tracker, onError }: ProfileTableProps) {
  return (
    <div
      className="overflow-x-auto rounded-lg border border-border"
      tabIndex={0}
      role="region"
      aria-label="Profiles table"
    >
      <table className="w-full text-sm">
        <caption className="sr-only">Colima profiles</caption>
        <thead>
          <tr className="border-b border-border text-left text-xs text-muted-foreground">
            {COLUMNS.map(([c, hide]) => (
              <th key={c} scope="col" className={`px-3 py-2 font-medium ${hide}`}>
                {c}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {profiles.map((p) => (
            <Row
              key={p.name}
              profile={p}
              tracker={tracker}
              unavailable={(p.warnings ?? []).some((w) => w.code === 'colima_unavailable')}
              onError={onError}
            />
          ))}
        </tbody>
      </table>
    </div>
  )
}

export function ProfileTableSkeleton() {
  return (
    <div className="overflow-x-auto rounded-lg border border-border">
      <table className="w-full text-sm" aria-busy="true">
        <caption className="sr-only">Loading profiles</caption>
        <tbody>
          {[0, 1, 2].map((i) => (
            <tr key={i} className="border-b border-border last:border-0">
              <td className="px-3 py-3">
                <div className="h-4 w-full animate-pulse rounded bg-muted" />
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
