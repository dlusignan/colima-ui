import { Link } from 'react-router-dom'
import { formatBytes } from '@/lib/format'
import type { Profile } from '@/lib/api/types'

const SOURCE_LABEL: Record<Profile['source'], string> = {
  both: 'both',
  cli_only: 'colima only',
  file_only: 'file only',
}

/** The backend sends 0 for "unknown". */
const orDash = (n: number, format: (n: number) => string) => (n > 0 ? format(n) : '—')

/** Configuration summary taken from the profile list. */
export function ProfileSummary({ profile }: { profile: Profile }) {
  const rows: [string, string][] = [
    ['Runtime', profile.runtime || '—'],
    ['CPUs', orDash(profile.cpus, String)],
    ['Memory', orDash(profile.memoryBytes, formatBytes)],
    ['Disk', orDash(profile.diskBytes, formatBytes)],
    ['Colima version', profile.colimaVersion || '—'],
    ['Source', SOURCE_LABEL[profile.source] ?? profile.source],
  ]
  return (
    <section aria-labelledby="summary-heading" className="flex flex-col gap-2">
      <div className="flex items-baseline justify-between">
        <h3 id="summary-heading" className="text-base font-semibold">
          Configuration
        </h3>
        <Link
          className="text-sm underline underline-offset-4"
          to={`/profiles/${encodeURIComponent(profile.name)}/config`}
        >
          Edit configuration
        </Link>
      </div>
      <dl className="grid grid-cols-[max-content_1fr] gap-x-6 gap-y-1 rounded-lg border border-border px-3 py-2 text-sm">
        {rows.map(([k, v]) => (
          <div key={k} className="contents">
            <dt className="text-muted-foreground">{k}</dt>
            <dd className="font-mono">{v}</dd>
          </div>
        ))}
      </dl>
    </section>
  )
}
