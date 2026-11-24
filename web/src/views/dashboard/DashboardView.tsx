import { Button } from '@/components/ui/button'
import { ApiError } from '@/lib/api/client'
import { useProfiles } from '@/lib/api/useProfiles'
import { useJobTracker } from '@/lib/jobs/useJobTracker'
import { useToast } from '@/lib/toast/useToast'
import { ProfileTable, ProfileTableSkeleton } from './ProfileTable'
import { WarningBanners } from '@/components/WarningBanners'
import { useCreateProfile } from '@/views/create/createProfileContext'

function listErrorText(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.status === 0) return 'Could not reach the server.'
    if (error.code === 'colima_failed') return 'Colima could not be queried.'
    return error.message
  }
  return 'Could not load the profiles.'
}

export function DashboardView() {
  const profiles = useProfiles()
  const list = profiles.data?.profiles
  const tracker = useJobTracker(list)
  const createProfile = useCreateProfile()
  const toast = useToast()

  const onError = (message: string) => toast.error({ title: message, key: `action-error:${message}` })

  let body
  if (profiles.isPending) {
    body = (
      <>
        <ProfileTableSkeleton />
        <p className="sr-only">Loading profiles</p>
      </>
    )
  } else if (!list) {
    body = (
      <div role="alert" className="flex items-center gap-3 rounded-lg border border-destructive px-3 py-2 text-sm">
        <span>{listErrorText(profiles.error)}</span>
        <Button size="xs" variant="outline" onClick={() => void profiles.refetch()}>
          Retry
        </Button>
      </div>
    )
  } else if (list.length === 0) {
    body = (
      <p className="rounded-lg border border-border px-3 py-4 text-sm text-muted-foreground">
        No Colima profiles found. Run <code className="font-mono">colima start</code> in a terminal to
        create the default one, then it appears here.
      </p>
    )
  } else {
    body = (
      <>
        {profiles.isError && (
          <div role="alert" className="flex items-center gap-3 rounded-lg border border-destructive px-3 py-2 text-sm">
            <span>{listErrorText(profiles.error)} Showing the last known state.</span>
            <Button size="xs" variant="outline" onClick={() => void profiles.refetch()}>
              Retry
            </Button>
          </div>
        )}
        <WarningBanners profiles={list} />
        <ProfileTable profiles={list} tracker={tracker} onError={onError} />
      </>
    )
  }

  return (
    <section className="flex flex-col gap-3">
      <div className="flex items-center justify-between gap-3">
        <h2 className="text-xl font-semibold">Dashboard</h2>
        <Button size="sm" onClick={createProfile.open}>
          New profile
        </Button>
      </div>
      {body}
    </section>
  )
}
