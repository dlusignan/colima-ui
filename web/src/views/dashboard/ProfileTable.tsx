import type { JobAction, Profile } from '@/lib/api/types'
import { useProfileAction } from '@/lib/jobs/useProfileAction'
import type { JobTracker } from '@/lib/jobs/useJobTracker'
import { ProfileRow } from './ProfileRow'

const COLUMNS = ['Name', 'Status', 'Runtime', 'CPUs', 'Memory', 'Disk', 'Colima', 'Source', 'Warnings', 'Actions']

export interface ActionFailure {
  id: number
  profile: string
  action: JobAction
  error: unknown
}

interface RowProps {
  profile: Profile
  tracker: JobTracker
  unavailable: boolean
  onFailure: (profile: string, action: JobAction, error: unknown) => void
}

function Row({ profile, tracker, unavailable, onFailure }: RowProps) {
  const mutation = useProfileAction(profile.name)
  return (
    <ProfileRow
      profile={profile}
      job={tracker.jobs.get(profile.name)}
      pending={mutation.isPending}
      unavailable={unavailable}
      onAction={(action) =>
        mutation.mutate(
          { action },
          {
            onSuccess: ({ jobId }) => tracker.adopt(profile.name, jobId),
            onError: (error) => onFailure(profile.name, action, error),
          },
        )
      }
      onDismiss={() => tracker.dismiss(profile.name)}
    />
  )
}

interface ProfileTableProps {
  profiles: readonly Profile[]
  tracker: JobTracker
  onFailure: RowProps['onFailure']
}

/** Wide table in a scroll container: Layout caps the page width. */
export function ProfileTable({ profiles, tracker, onFailure }: ProfileTableProps) {
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
            {COLUMNS.map((c) => (
              <th key={c} scope="col" className="px-3 py-2 font-medium">
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
              onFailure={onFailure}
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
