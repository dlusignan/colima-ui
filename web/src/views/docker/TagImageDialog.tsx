import { useId, useState, type FormEvent } from 'react'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { ApiError } from '@/lib/api/client'
import { api } from '@/lib/api/endpoints'
import type { DockerImage, TagImageRequest } from '@/lib/api/types'
import { buildTagRequest, imageLabel, resultingReference, shortImageId, splitReference, validateTagForm } from './imageRules'
import { useDockerMutation } from './useDockerMutation'

interface ServerErrors {
  repository?: string
  tag?: string
  /** A problem that belongs to no field (the image itself). */
  form?: string
}

/** Maps the 404, 409 and 422 answers of the tag route to the field they belong to. Other errors are not handled here. */
function serverErrorsFrom(error: unknown): ServerErrors | null {
  if (!(error instanceof ApiError)) return null
  if (error.status === 404 && error.code === 'not_found') {
    return { form: 'This image no longer exists. Close this dialog and check the list.' }
  }
  if (error.status === 409 && error.code === 'conflict') {
    return { tag: 'This tag already points at another image. Choose another tag. Moving a tag is not offered here.' }
  }
  if (error.status === 422) {
    const fields = error.details.fields
    const out: ServerErrors = {}
    if (typeof fields === 'object' && fields !== null) {
      const f = fields as Record<string, unknown>
      if (typeof f.repository === 'string' && f.repository) out.repository = f.repository
      if (typeof f.tag === 'string' && f.tag) out.tag = f.tag
      if (typeof f.image === 'string' && f.image) out.form = 'Docker does not know this image by that ID. Close this dialog and check the list.'
    }
    if (!out.repository && !out.tag && !out.form) out.form = 'Docker rejected this request. Check the repository and the tag.'
    return out
  }
  return null
}

const errorText = 'text-xs text-destructive'

/** Dialog to add a tag to one image. Nothing is sent until the form is valid. */
export function TagImageDialog({
  profile,
  image,
  onClose,
}: {
  profile: string
  /** The image to tag; null keeps the dialog closed. */
  image: DockerImage | null
  onClose: () => void
}) {
  const [server, setServer] = useState<ServerErrors>({})
  const close = () => {
    setServer({})
    onClose()
  }
  const mutation = useDockerMutation({
    profile,
    section: 'images',
    action: 'image-tag',
    mutationFn: (v: { id: string; body: TagImageRequest }) => api.tagImage(profile, v.id, v.body),
    successTitle: (_d, v) => `Tagged image ${v.id} as ${v.body.repository}:${v.body.tag}`,
    errorTitle: (v) => `Could not tag image ${v.id}`,
    onSuccess: close,
    handleError: (error) => {
      const mapped = serverErrorsFrom(error)
      if (!mapped) return false
      setServer(mapped)
      return true
    },
  })
  return (
    <Dialog open={image !== null} onOpenChange={(next) => !next && close()}>
      <DialogContent className="top-[8%] max-h-[85vh] max-w-lg overflow-y-auto">
        {image && (
          <>
            <DialogTitle>Tag {imageLabel(image)}</DialogTitle>
            <DialogDescription>
              Adds one more name to image {shortImageId(image.id)}. The image and its other tags stay as they are. The
              server never moves a tag from another image.
            </DialogDescription>
            <TagImageForm
              key={image.id}
              image={image}
              server={server}
              clearServer={(field) => setServer((s) => ({ ...s, [field]: undefined }))}
              pending={mutation.isPending}
              onCancel={close}
              onSubmit={(body) => {
                setServer({})
                mutation.run({ id: shortImageId(image.id), body })
              }}
            />
          </>
        )}
      </DialogContent>
    </Dialog>
  )
}

function TagImageForm({
  image,
  server,
  clearServer,
  pending,
  onCancel,
  onSubmit,
}: {
  image: DockerImage
  server: ServerErrors
  clearServer: (field: keyof ServerErrors) => void
  pending: boolean
  onCancel: () => void
  onSubmit: (body: TagImageRequest) => void
}) {
  // The repository of the first tag is the usual starting point (a new version of the same name); the tag is new.
  const [repository, setRepository] = useState(() => (image.tags[0] ? splitReference(image.tags[0]).repository : ''))
  const [tag, setTag] = useState('')
  const [submitted, setSubmitted] = useState(false)
  const repositoryId = useId()
  const tagId = useId()
  const repositoryErrorId = useId()
  const tagErrorId = useId()
  const formErrorId = useId()
  const previewId = useId()
  const errors = validateTagForm(repository, tag, image.tags)
  const repositoryError = (submitted ? errors.repository : undefined) ?? server.repository
  const tagError = (submitted ? errors.tag : undefined) ?? server.tag
  const reference = resultingReference(repository, tag)

  function submit(e: FormEvent) {
    e.preventDefault()
    setSubmitted(true)
    if (pending || errors.repository || errors.tag) return
    onSubmit(buildTagRequest(repository, tag))
  }

  return (
    <form onSubmit={submit} noValidate className="flex flex-col gap-4">
      <div className="flex flex-col gap-1.5 text-sm">
        <label htmlFor={repositoryId}>Repository</label>
        <Input
          id={repositoryId}
          value={repository}
          onChange={(e) => {
            clearServer('repository')
            setRepository(e.target.value)
          }}
          placeholder="registry.example.com:5000/team/app"
          autoComplete="off"
          spellCheck={false}
          autoFocus={repository === ''}
          aria-invalid={repositoryError ? true : undefined}
          aria-describedby={repositoryError ? repositoryErrorId : undefined}
        />
        {repositoryError && (
          <p id={repositoryErrorId} className={errorText}>
            {repositoryError}
          </p>
        )}
      </div>
      <div className="flex flex-col gap-1.5 text-sm">
        <label htmlFor={tagId}>Tag</label>
        <Input
          id={tagId}
          value={tag}
          onChange={(e) => {
            clearServer('tag')
            setTag(e.target.value)
          }}
          placeholder="v2"
          autoComplete="off"
          spellCheck={false}
          autoFocus={repository !== ''}
          aria-invalid={tagError ? true : undefined}
          aria-describedby={tagError ? tagErrorId : undefined}
        />
        {tagError && (
          <p id={tagErrorId} className={errorText}>
            {tagError}
          </p>
        )}
      </div>
      <p id={previewId} className="text-sm" aria-live="polite">
        <span className="text-muted-foreground">Resulting reference: </span>
        {reference ? <span className="font-mono break-all">{reference}</span> : <span className="text-muted-foreground">enter a repository and a tag</span>}
      </p>
      {server.form && (
        <p id={formErrorId} role="alert" className={errorText}>
          {server.form}
        </p>
      )}
      <div className="flex justify-end gap-2">
        <Button type="button" variant="outline" onClick={onCancel}>
          Cancel
        </Button>
        <Button type="submit" disabled={pending}>
          {pending ? 'Tagging…' : 'Add tag'}
        </Button>
      </div>
    </form>
  )
}
