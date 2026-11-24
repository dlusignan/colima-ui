import { useId, useRef, useState, type RefObject } from 'react'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Input } from '@/components/ui/input'
import { formatBytes } from '@/lib/format'
import type { DockerImage } from '@/lib/api/types'
import { pruneCandidates, shortImageId, type PruneMode } from './imageRules'
import { plural } from './volumeText'

/** The text the user types before every unused image is removed. */
export const PRUNE_ALL_TEXT = 'prune'

/**
 * Prune with two choices. "Dangling images only" is the default and needs a
 * plain confirmation; "All unused images" also removes tagged images and needs
 * the word `prune` typed. Each choice lists what the loaded list says would go,
 * which is a lower bound: Docker decides what is unused when the call runs.
 */
export function PruneImagesDialog({
  images,
  open,
  onOpenChange,
  onConfirm,
}: {
  images: readonly DockerImage[]
  open: boolean
  onOpenChange: (open: boolean) => void
  /** Runs after the user confirmed; `all` is the choice that was made. */
  onConfirm: (all: boolean) => void
}) {
  const cancelRef = useRef<HTMLButtonElement>(null)
  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent initialFocus={cancelRef}>
        <PruneBody images={images} cancelRef={cancelRef} onClose={() => onOpenChange(false)} onConfirm={onConfirm} />
      </AlertDialogContent>
    </AlertDialog>
  )
}

function PruneBody({
  images,
  cancelRef,
  onClose,
  onConfirm,
}: {
  images: readonly DockerImage[]
  cancelRef: RefObject<HTMLButtonElement | null>
  onClose: () => void
  onConfirm: (all: boolean) => void
}) {
  const [mode, setMode] = useState<PruneMode>('dangling')
  const [typed, setTyped] = useState('')
  const legendId = useId()
  const inputId = useId()
  const reasonId = useId()
  const name = useId()
  const all = mode === 'all'
  const candidates = pruneCandidates(images, mode)
  const total = candidates.reduce((sum, i) => sum + i.sizeBytes, 0)
  const empty = candidates.length === 0
  const typedOk = !all || typed === PRUNE_ALL_TEXT
  const emptyReason = all ? 'No unused images in the list on screen.' : 'No dangling images in the list on screen.'

  return (
    <>
      <AlertDialogHeader>
        <AlertDialogTitle>Prune images?</AlertDialogTitle>
        <AlertDialogDescription>
          This cannot be undone. Images that any container uses, running or stopped, are never removed.
        </AlertDialogDescription>
      </AlertDialogHeader>
      <fieldset aria-labelledby={legendId} className="flex flex-col gap-2 text-sm">
        <legend id={legendId} className="mb-1 font-medium">
          What to prune
        </legend>
        <label className="flex items-start gap-2">
          <input
            type="radio"
            name={name}
            className="mt-1 accent-primary"
            checked={!all}
            onChange={() => {
              setMode('dangling')
              setTyped('')
            }}
          />
          <span>
            Dangling images only
            <span className="block text-xs text-muted-foreground">Images with no tag that no container uses.</span>
          </span>
        </label>
        <label className="flex items-start gap-2">
          <input type="radio" name={name} className="mt-1 accent-primary" checked={all} onChange={() => setMode('all')} />
          <span>
            All unused images
            <span className="block text-xs text-muted-foreground">
              Every image no container uses, tagged images included. Pull or build them again to get them back.
            </span>
          </span>
        </label>
      </fieldset>
      <div className="flex flex-col gap-2 text-sm">
        {empty ? (
          <p id={reasonId} className="text-muted-foreground">
            {emptyReason}
          </p>
        ) : (
          <>
            <p>
              The list on screen says {plural(candidates.length, 'image', 'images')}, {formatBytes(total)} in total, would be removed.
            </p>
            <ul aria-label="Images that would be removed" className="max-h-40 list-inside list-disc overflow-y-auto">
              {candidates.map((i) => (
                <li key={i.id} className="break-all font-mono">
                  {i.tags.length > 0 ? i.tags.join(', ') : `<none> ${shortImageId(i.id)}`} ({formatBytes(i.sizeBytes)})
                </li>
              ))}
            </ul>
          </>
        )}
        <p className="text-xs text-muted-foreground">
          This is a lower bound. Docker decides what is unused when you confirm, so it can remove more, for example
          images this list does not show or created since it loaded. Layers shared with other images are freed only once,
          so the space freed can be less than the total.
        </p>
      </div>
      {all && (
        <div className="flex flex-col gap-1.5 text-sm">
          <label htmlFor={inputId}>
            Type <span className="font-mono font-semibold">{PRUNE_ALL_TEXT}</span> to confirm
          </label>
          <Input
            id={inputId}
            value={typed}
            onChange={(e) => setTyped(e.target.value)}
            autoComplete="off"
            spellCheck={false}
          />
        </div>
      )}
      <AlertDialogFooter>
        <AlertDialogCancel ref={cancelRef}>Cancel</AlertDialogCancel>
        <AlertDialogAction
          variant="destructive"
          disabled={empty || !typedOk}
          aria-describedby={empty ? reasonId : undefined}
          onClick={() => {
            onClose()
            onConfirm(all)
          }}
        >
          {all ? 'Prune all unused images' : 'Prune dangling images'}
        </AlertDialogAction>
      </AlertDialogFooter>
    </>
  )
}
