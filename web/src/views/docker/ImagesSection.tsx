import { useQueryClient } from '@tanstack/react-query'
import { ApiError } from '@/lib/api/client'
import { queryKeys } from '@/lib/api/keys'
import { useImages } from '@/lib/api/useImages'
import { formatBytes, formatRelativeTime } from '@/lib/format'
import type { Profile } from '@/lib/api/types'

const note = 'rounded-lg border border-border px-3 py-3 text-sm text-muted-foreground'

function errorText(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.code === 'docker_unavailable') return 'Docker is not reachable in this VM yet.'
    if (error.status === 0) return 'Could not reach the server.'
  }
  return 'Images could not be loaded.'
}

/** Images of a running VM. Nothing is requested while the VM is not running. */
export function ImagesSection({ profile }: { profile: Profile }) {
  const running = profile.status === 'running'
  const query = useImages(profile.name, running)
  const queryClient = useQueryClient()
  const list = query.data?.images

  let body
  if (!running) {
    body = <p className={note}>Start the profile to see its images.</p>
  } else if (query.isPending) {
    body = <p className={note}>Loading images…</p>
  } else if (query.isError && !list) {
    const unavailable = query.error instanceof ApiError && query.error.code === 'docker_unavailable'
    body = (
      <div role="alert" className="flex items-center justify-between gap-2 rounded-lg border border-destructive px-3 py-2 text-sm">
        <span>{errorText(query.error)}</span>
        {!unavailable && (
          <button
            type="button"
            className="underline underline-offset-4"
            onClick={() => void queryClient.invalidateQueries({ queryKey: queryKeys.images(profile.name) })}
          >
            Retry
          </button>
        )}
      </div>
    )
  } else if (!list || list.length === 0) {
    body = <p className={note}>No images.</p>
  } else {
    body = (
      <div className="overflow-x-auto rounded-lg border border-border" tabIndex={0} role="region" aria-label="Images table">
        <table className="w-full text-sm">
          <caption className="sr-only">Images in {profile.name}</caption>
          <thead>
            <tr className="border-b border-border text-left text-xs text-muted-foreground">
              {['Tags', 'Image ID', 'Size', 'Created', 'Used by'].map((c) => (
                <th key={c} scope="col" className="px-3 py-2 font-medium">
                  {c}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {list.map((img) => {
              const created = new Date(img.createdAt)
              return (
                <tr key={img.id} className="border-b border-border last:border-0 align-top">
                  <th scope="row" className="px-3 py-2 text-left font-mono font-medium">
                    {img.tags.length === 0 ? (
                      <div>&lt;none&gt;</div>
                    ) : (
                      img.tags.map((t) => <div key={t}>{t}</div>)
                    )}
                  </th>
                  <td className="px-3 py-2 font-mono">{img.id.replace(/^sha256:/, '').slice(0, 12)}</td>
                  <td className="px-3 py-2 font-mono whitespace-nowrap">{formatBytes(img.sizeBytes)}</td>
                  <td className="px-3 py-2 whitespace-nowrap">
                    <span title={Number.isNaN(created.getTime()) ? undefined : created.toLocaleString()}>
                      {formatRelativeTime(img.createdAt)}
                    </span>
                  </td>
                  <td className="px-3 py-2 whitespace-nowrap">
                    {img.containers === 0 ? 'Unused' : img.containers}
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
    <section aria-labelledby="images-heading" className="flex flex-col gap-2">
      <h3 id="images-heading" className="text-base font-semibold">
        Images
      </h3>
      {body}
    </section>
  )
}
