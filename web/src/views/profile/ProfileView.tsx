import { useEffect } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { useQueryClient } from '@tanstack/react-query'
import { ProfileActions } from '@/components/ProfileActions'
import { StatusDot } from '@/components/StatusDot'
import { WarningBanners } from '@/components/WarningBanners'
import { Button } from '@/components/ui/button'
import { ApiError } from '@/lib/api/client'
import { useProfiles } from '@/lib/api/useProfiles'
import type { JobAction } from '@/lib/api/types'
import { queryKeys } from '@/lib/api/keys'
import { describeActionError } from '@/lib/jobs/describeActionError'
import { useDeleteProfile } from '@/lib/jobs/useDeleteProfile'
import { useJobTracker } from '@/lib/jobs/useJobTracker'
import { useProfileActionRunner } from '@/lib/jobs/useProfileActionRunner'
import { useToast } from '@/lib/toast/useToast'
import { logPath } from '@/views/log/logPath'
import { ActionsPanel } from './ActionsPanel'
import { ContainersPanel } from './ContainersPanel'
import { DeleteProfileDialog } from './DeleteProfileDialog'
import { ProfileSummary } from './ProfileSummary'
import { SshConfigDialog } from './SshConfigDialog'

const PROGRESS: Record<JobAction, string> = {
  start: 'starting',
  stop: 'stopping',
  restart: 'restarting',
  delete: 'deleting',
  update: 'updating',
  create: 'creating',
  'image-pull': 'pulling',
}

function listErrorText(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.status === 0) return 'Could not reach the server.'
    if (error.code === 'colima_failed') return 'Colima could not be queried.'
    return error.message
  }
  return 'Could not load the profile.'
}

export function ProfileView() {
  const { name = '' } = useParams()
  const profiles = useProfiles()
  const list = profiles.data?.profiles
  const tracker = useJobTracker(list)
  const toast = useToast()
  const runner = useProfileActionRunner(name, tracker, (message) =>
    toast.error({ title: message, key: `action-error:${message}` }),
  )
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const deletion = useDeleteProfile(name)
  const deleteJob = tracker.jobs.get(name)
  const deleted = deleteJob?.action === 'delete' && deleteJob.state === 'succeeded'

  // The profile is gone: leave its page. The job toast stays on screen.
  useEffect(() => {
    if (deleted) navigate('/', { replace: true })
  }, [deleted, navigate])

  const requestDelete = (deleteData: boolean) => {
    if (deletion.isPending) return
    deletion.mutate(
      { deleteData },
      {
        onSuccess: ({ jobId }) => tracker.adopt(name, jobId),
        onError: (error) => {
          const d = describeActionError(error, 'delete', name)
          if (d.trackJobId) tracker.adopt(name, d.trackJobId)
          if (d.refresh) void queryClient.invalidateQueries({ queryKey: queryKeys.profiles })
          if (d.message) toast.error({ title: d.message, key: `action-error:${d.message}` })
        },
      },
    )
  }

  if (profiles.isPending) {
    return (
      <p aria-busy="true" className="text-sm text-muted-foreground">
        Loading profile…
      </p>
    )
  }
  if (!list) {
    return (
      <div role="alert" className="flex items-center gap-3 rounded-lg border border-destructive px-3 py-2 text-sm">
        <span>{listErrorText(profiles.error)}</span>
        <Button size="xs" variant="outline" onClick={() => void profiles.refetch()}>
          Retry
        </Button>
      </div>
    )
  }
  const profile = list.find((p) => p.name === name)
  if (!profile) {
    return (
      <div className="flex flex-col gap-2 rounded-lg border border-border px-3 py-4 text-sm">
        <h3 className="text-base font-semibold">404</h3>
        <p>No profile named {name}.</p>
        <Link className="underline underline-offset-4" to="/">
          Back to the dashboard
        </Link>
      </div>
    )
  }

  const job = tracker.jobs.get(name)
  const running = job?.state === 'running'
  const busy = runner.pending || deletion.isPending || running || profile.activeJobId != null
  const unavailable = (profile.warnings ?? []).some((w) => w.code === 'colima_unavailable')
  const logJobId = job?.jobId ?? profile.activeJobId

  return (
    <div className="flex flex-col gap-4">
      {profiles.isError && (
        <div role="alert" className="flex items-center gap-3 rounded-lg border border-destructive px-3 py-2 text-sm">
          <span>{listErrorText(profiles.error)} Showing the last known state.</span>
          <Button size="xs" variant="outline" onClick={() => void profiles.refetch()}>
            Retry
          </Button>
        </div>
      )}
      <WarningBanners profiles={[profile]} />
      <section aria-label="Status" className="flex flex-col gap-2 rounded-lg border border-border px-3 py-3">
        <div className="flex flex-wrap items-center gap-4">
          <StatusDot status={profile.status} label={running && job ? PROGRESS[job.action] : undefined} />
          <ProfileActions profile={profile} busy={busy} unavailable={unavailable} onAction={runner.run} showUpdate />
          <SshConfigDialog profile={name} />
          {(job || profile.activeJobId) && (
            <Link className="text-sm underline underline-offset-4" to={logPath(name, logJobId)}>
              Open log
            </Link>
          )}
        </div>
        {(profile.warnings ?? [])
          .filter((w) => w.code !== 'colima_unavailable' && w.code !== 'colima_version_unsupported')
          .map((w) => (
            <p key={w.code} role="status" className="text-sm text-warning">
              {w.message}
            </p>
          ))}
      </section>
      <ProfileSummary profile={profile} />
      <ContainersPanel profile={profile} />
      <ActionsPanel profile={name} />
      <section aria-labelledby="danger-heading" className="flex flex-col gap-2">
        <h3 id="danger-heading" className="text-base font-semibold">
          Danger zone
        </h3>
        <DeleteProfileDialog
          profile={profile}
          busy={busy}
          unavailable={unavailable}
          onDelete={requestDelete}
        />
      </section>
    </div>
  )
}
