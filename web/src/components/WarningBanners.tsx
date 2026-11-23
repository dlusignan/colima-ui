import type { Profile } from '@/lib/api/types'

/** One banner per kind instead of one per profile. Server text is plain text. */
export function WarningBanners({ profiles }: { profiles: readonly Profile[] }) {
  const unavailable = profiles.some((p) => p.warnings?.some((w) => w.code === 'colima_unavailable'))
  const versions = new Map<string, string>()
  for (const p of profiles) {
    const w = p.warnings?.find((x) => x.code === 'colima_version_unsupported')
    if (w && !versions.has(p.colimaVersion)) versions.set(p.colimaVersion, w.message)
  }
  if (!unavailable && versions.size === 0) return null
  const cls = 'rounded-lg border border-warning px-3 py-2 text-sm text-warning'
  return (
    <div className="flex flex-col gap-2">
      {unavailable && (
        <p role="status" className={cls}>
          Colima could not be queried. Showing configuration only. Actions are disabled.
        </p>
      )}
      {[...versions].map(([version, message]) => (
        <p key={version} role="status" className={cls}>
          {message}
        </p>
      ))}
    </div>
  )
}
