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
import type { DockerImage } from '@/lib/api/types'
import { shortImageId } from './imageRules'

/** What the user picked: untag one reference (`{ tag }`) or remove the image with every tag (`{ tag: null }`). */
export interface RemoveChoice {
  /** The exact reference to remove, or null for the whole image. */
  tag: string | null
}

const IMAGE = '\u0000image'

/**
 * Confirmation for removing an image. An image with several tags offers the
 * choice between removing one tag (the image stays while other tags exist) and
 * removing the image with all its tags. With one tag or none there is nothing
 * to choose and the image is removed by its id.
 */
export function RemoveImageDialog({
  image,
  onClose,
  onConfirm,
}: {
  /** The image to remove; null keeps the dialog closed. */
  image: DockerImage | null
  onClose: () => void
  onConfirm: (image: DockerImage, choice: RemoveChoice) => void
}) {
  const cancelRef = useRef<HTMLButtonElement>(null)
  return (
    <AlertDialog open={image !== null} onOpenChange={(next) => !next && onClose()}>
      <AlertDialogContent initialFocus={cancelRef}>
        {image && <RemoveBody key={image.id} image={image} cancelRef={cancelRef} onClose={onClose} onConfirm={onConfirm} />}
      </AlertDialogContent>
    </AlertDialog>
  )
}

function RemoveBody({
  image,
  cancelRef,
  onClose,
  onConfirm,
}: {
  image: DockerImage
  cancelRef: RefObject<HTMLButtonElement | null>
  onClose: () => void
  onConfirm: (image: DockerImage, choice: RemoveChoice) => void
}) {
  const legendId = useId()
  const name = useId()
  const id = shortImageId(image.id)
  const several = image.tags.length > 1
  const [picked, setPicked] = useState<string>(image.tags[0] ?? IMAGE)
  // The list can change while the dialog is open; a tag that is gone falls back to the first one.
  const selected = picked === IMAGE || image.tags.includes(picked) ? picked : (image.tags[0] ?? IMAGE)
  const untagging = several && selected !== IMAGE

  let title: string
  let description: string
  let confirmLabel: string
  if (several) {
    title = `Remove image ${id}?`
    description =
      `This image has ${image.tags.length} tags. Removing one tag keeps the image while other tags point at it. ` +
      'Removing the image removes all its tags and deletes it. Containers that use it block the removal either way.'
    confirmLabel = untagging ? 'Remove tag' : 'Remove image'
  } else {
    title = `Remove image ${image.tags[0] ?? id}?`
    description =
      `The image ${image.tags[0] ? `${image.tags[0]} (${id})` : id} is deleted from this VM. ` +
      'Containers that use it block the removal. Pull or build it again to get it back.'
    confirmLabel = 'Remove image'
  }

  return (
    <>
      <AlertDialogHeader>
        <AlertDialogTitle>{title}</AlertDialogTitle>
        <AlertDialogDescription>{description}</AlertDialogDescription>
      </AlertDialogHeader>
      {several && (
        <fieldset aria-labelledby={legendId} className="flex max-h-56 flex-col gap-2 overflow-y-auto text-sm">
          <legend id={legendId} className="mb-1 font-medium">
            What to remove
          </legend>
          {image.tags.map((t) => (
            <label key={t} className="flex items-start gap-2">
              <input
                type="radio"
                name={name}
                className="mt-1 accent-primary"
                checked={selected === t}
                onChange={() => setPicked(t)}
              />
              <span>
                Remove only the tag <span className="font-mono break-all">{t}</span>
              </span>
            </label>
          ))}
          <label className="flex items-start gap-2">
            <input
              type="radio"
              name={name}
              className="mt-1 accent-primary"
              checked={selected === IMAGE}
              onChange={() => setPicked(IMAGE)}
            />
            <span>Remove the image and all {image.tags.length} tags</span>
          </label>
        </fieldset>
      )}
      <AlertDialogFooter>
        <AlertDialogCancel ref={cancelRef}>Cancel</AlertDialogCancel>
        <AlertDialogAction
          variant="destructive"
          onClick={() => {
            onClose()
            onConfirm(image, { tag: untagging ? selected : null })
          }}
        >
          {confirmLabel}
        </AlertDialogAction>
      </AlertDialogFooter>
    </>
  )
}
