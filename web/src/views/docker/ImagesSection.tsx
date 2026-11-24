import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { ApiError } from '@/lib/api/client'
import { api } from '@/lib/api/endpoints'
import { queryKeys } from '@/lib/api/keys'
import { useImages } from '@/lib/api/useImages'
import { formatBytes, formatRelativeTime } from '@/lib/format'
import { useToast } from '@/lib/toast/useToast'
import type { DockerImage, Profile } from '@/lib/api/types'
import type { DockerAction } from './ActionButton'
import { imageLabel, imageRemoveReason, pruneCandidates, shortImageId } from './imageRules'
import { containersInUse, imagePruneTitle, partialRemoval, removeTitle } from './imageText'
import { ImageInspectDialog } from './ImageInspectDialog'
import { PruneImagesDialog } from './PruneImagesDialog'
import { PullBanner } from './PullBanner'
import { PullImageDialog } from './PullImageDialog'
import { RemoveImageDialog, type RemoveChoice } from './RemoveImageDialog'
import { RowActions } from './RowActions'
import { SectionError, noteClass } from './SectionState'
import { SectionToolbar } from './SectionToolbar'
import { TagImageDialog } from './TagImageDialog'
import { useDockerMutation } from './useDockerMutation'
import { usePullJobs } from './usePullJobs'
import { plural } from './volumeText'

interface RemoveVars {
  /** The exact reference when one tag is removed, otherwise the 12-character id. */
  target: string
  label: string
}

/** Images of a running VM. Nothing is requested while the VM is not running. */
export function ImagesSection({ profile }: { profile: Profile }) {
  const running = profile.status === 'running'
  const query = useImages(profile.name, running)
  const queryClient = useQueryClient()
  const toast = useToast()
  const list = query.data?.images
  // Dialogs hold the image id and read the row from the current list, so they follow its refreshes.
  const [tagging, setTagging] = useState<string | null>(null)
  const [inspecting, setInspecting] = useState<string | null>(null)
  const [removing, setRemoving] = useState<string | null>(null)
  const [removingNow, setRemovingNow] = useState<string | null>(null)
  const [pruning, setPruning] = useState(false)
  const [pulling, setPulling] = useState(false)
  const pulls = usePullJobs(profile.name)
  const byId = (id: string | null): DockerImage | null =>
    id === null ? null : ((list ?? []).find((i) => shortImageId(i.id) === id) ?? null)

  const remove = useDockerMutation({
    profile: profile.name,
    section: 'images',
    action: 'image-remove',
    mutationFn: (v: RemoveVars) => api.removeImage(profile.name, v.target),
    successTitle: (result) => removeTitle(result),
    errorTitle: (v) => `Could not remove ${v.label}`,
    handleError: (error, v) => {
      // A removal by id takes several requests: a failure part-way (409 or 502) reports what already happened.
      if (!(error instanceof ApiError)) return false
      const partial = partialRemoval(error.details)
      const used = error.status === 409 ? containersInUse(error.details) : null
      const parts: string[] = []
      if (partial) {
        const done = [...partial.untagged, ...partial.deleted.map((id) => `image ${id}`)]
        parts.push(`Part of the removal happened before it failed: ${done.join(', ')}. The list is refreshed.`)
      }
      if (used !== null) {
        parts.push(`${plural(used, 'container uses', 'containers use')} this image, running or stopped. Remove those containers first.`)
      } else if (!partial) {
        return false
      }
      toast.error({
        title: `Could not remove ${v.label}`,
        description: parts.join(' '),
        key: `docker:${profile.name}:image-remove:${error.status}:${partial ? 'partial' : 'used'}`,
      })
      return true
    },
  })
  const prune = useDockerMutation({
    profile: profile.name,
    section: 'images',
    action: 'image-prune',
    mutationFn: (all: boolean) => api.pruneImages(profile.name, all),
    successTitle: (result) => imagePruneTitle(result),
    errorTitle: () => 'Could not prune images',
  })

  const unused = pruneCandidates(list ?? [], 'all')
  const toolbarActions: DockerAction[] = [
    { id: 'pull', label: 'Pull image', onSelect: () => setPulling(true) },
    {
      id: 'prune',
      label: 'Prune images',
      onSelect: () => setPruning(true),
      pending: prune.isPending,
      disabledReason: !list ? 'Images are not loaded' : unused.length === 0 ? 'No unused images' : undefined,
    },
  ]

  function startRemove(img: DockerImage, choice: RemoveChoice) {
    const id = shortImageId(img.id)
    setRemovingNow(id)
    // A reference only for one tag of several, otherwise the id: removing by id takes every tag with it.
    if (choice.tag !== null) remove.run({ target: choice.tag, label: `tag ${choice.tag}` })
    else remove.run({ target: id, label: img.tags.length > 1 ? `image ${id}` : imageLabel(img) })
  }

  let body
  if (!running) {
    body = <p className={noteClass}>Start the profile to see its images.</p>
  } else if (query.isPending) {
    body = <p className={noteClass}>Loading images…</p>
  } else if (query.isError && !list) {
    body = (
      <SectionError
        error={query.error}
        fallback="Images could not be loaded."
        onRetry={() => void queryClient.invalidateQueries({ queryKey: queryKeys.images(profile.name) })}
      />
    )
  } else if (!list || list.length === 0) {
    body = <p className={noteClass}>No images.</p>
  } else {
    body = (
      <div className="overflow-x-auto rounded-lg border border-border" tabIndex={0} role="region" aria-label="Images table">
        <table className="w-full text-sm">
          <caption className="sr-only">Images in {profile.name}</caption>
          <thead>
            <tr className="border-b border-border text-left text-xs text-muted-foreground">
              {['Tags', 'Image ID', 'Size', 'Created', 'Used by', 'Actions'].map((c) => (
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
                  <td className="px-3 py-2">
                    <RowActions
                      object={img.tags[0] ?? `image ${shortImageId(img.id)}`}
                      actions={[
                        { id: 'inspect', label: 'Inspect', onSelect: () => setInspecting(shortImageId(img.id)) },
                        { id: 'tag', label: 'Tag', onSelect: () => setTagging(shortImageId(img.id)) },
                        {
                          id: 'remove',
                          label: 'Remove',
                          onSelect: () => setRemoving(shortImageId(img.id)),
                          pending: remove.isPending && removingNow === shortImageId(img.id),
                          disabledReason: imageRemoveReason(img),
                        },
                      ]}
                    />
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
      <SectionToolbar
        label="Image actions"
        actions={toolbarActions}
        disabledReason={running ? undefined : 'Start the profile to use these actions.'}
      />
      <PullBanner profile={profile.name} pulls={pulls.running} onCancel={pulls.cancel} />
      {body}
      <PullImageDialog
        profile={profile.name}
        open={pulling}
        onOpenChange={setPulling}
        onStarted={pulls.track}
      />
      <TagImageDialog profile={profile.name} image={byId(tagging)} onClose={() => setTagging(null)} />
      <ImageInspectDialog profile={profile.name} image={inspecting} onClose={() => setInspecting(null)} />
      <RemoveImageDialog image={byId(removing)} onClose={() => setRemoving(null)} onConfirm={startRemove} />
      <PruneImagesDialog images={list ?? []} open={pruning} onOpenChange={setPruning} onConfirm={(all) => prune.run(all)} />
    </section>
  )
}
