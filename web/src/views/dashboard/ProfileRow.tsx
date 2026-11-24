import { Link } from 'react-router-dom'
import { StatusDot } from '@/components/StatusDot'
import { formatBytes } from '@/lib/format'
import type { JobAction, Profile, ProfileAction } from '@/lib/api/types'
import type { TrackedJob } from '@/lib/jobs/useJobTracker'
import { logPath } from '@/views/log/logPath'
import { ProfileActions } from '@/components/ProfileActions'

const PROGRESS: Record<JobAction, string> = {
  start: 'starting',
  stop: 'stopping',
  restart: 'restarting',
  delete: 'deleting',
  update: 'updating',
  create: 'creating',
  'image-pull': 'pulling',
}

const WARNING_LABEL: Record<string, string> = {
  colima_unavailable: 'Colima unreachable',
  colima_version_unsupported: 'Version',
  config_unreadable: 'Config',
  config_invalid: 'Config',
}

const SOURCE_LABEL: Record<Profile['source'], string> = {
  both: 'both',
  cli_only: 'colima only',
  file_only: 'file only',
}

/** The backend sends 0 for "unknown". */
const orDash = (n: number, format: (n: number) => string) => (n > 0 ? format(n) : '—')

interface ProfileRowProps {
  profile: Profile
  job: TrackedJob | undefined
  pending: boolean
  unavailable: boolean
  onAction: (action: ProfileAction) => void
}

export function ProfileRow({ profile, job, pending, unavailable, onAction }: ProfileRowProps) {
  const running = job?.state === 'running'
  const busy = pending || running || profile.activeJobId != null
  const label = running && job ? PROGRESS[job.action] : undefined
  const logJobId = job?.jobId ?? profile.activeJobId

  return (
    <tr data-profile={profile.name} className="border-b border-border last:border-0">
      <th scope="row" className="px-3 py-2 text-left font-mono font-medium">
        <Link className="underline-offset-4 hover:underline" to={`/profiles/${encodeURIComponent(profile.name)}`}>
          {profile.name}
        </Link>
      </th>
      <td className="px-3 py-2">
        <StatusDot status={profile.status} label={label} />
      </td>
      <td className="px-3 py-2 font-mono">{profile.runtime || '—'}</td>
      <td className="px-3 py-2 font-mono">{orDash(profile.cpus, String)}</td>
      <td className="px-3 py-2 font-mono">{orDash(profile.memoryBytes, formatBytes)}</td>
      <td className="px-3 py-2 font-mono">{orDash(profile.diskBytes, formatBytes)}</td>
      <td className="px-3 py-2 font-mono">{profile.colimaVersion || '—'}</td>
      <td className="px-3 py-2">{SOURCE_LABEL[profile.source] ?? profile.source}</td>
      <td className="px-3 py-2">
        <ul className="flex flex-wrap gap-1">
          {(profile.warnings ?? []).map((w) => (
            <li
              key={w.code}
              title={w.message}
              className="rounded border border-warning px-1 text-xs text-warning"
            >
              {WARNING_LABEL[w.code] ?? w.code}
              <span className="sr-only">: {w.message}</span>
            </li>
          ))}
        </ul>
      </td>
      <td className="px-3 py-2">
        <div className="flex items-center gap-2">
          <ProfileActions profile={profile} busy={busy} unavailable={unavailable} onAction={onAction} />
          {(job || profile.activeJobId) && (
            <Link
              className="text-xs underline underline-offset-4"
              to={logPath(profile.name, logJobId)}
              aria-label={`Log ${profile.name}`}
            >
              Log
            </Link>
          )}
        </div>
      </td>
    </tr>
  )
}
