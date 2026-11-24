import { useId, useState, type FormEvent } from 'react'
import { useMutation } from '@tanstack/react-query'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { ApiError } from '@/lib/api/client'
import { api } from '@/lib/api/endpoints'
import { useToast } from '@/lib/toast/useToast'
import { describeDockerError } from './describeDockerError'
import { validatePullReference } from './imageRules'

interface ServerErrors {
  reference?: string
  /** A problem that belongs to no field. */
  form?: string
}

/** Maps the 409 and 422 answers of the pull route to the field or message they belong to. Other errors become a toast. */
function serverErrorsFrom(error: unknown): ServerErrors | null {
  if (!(error instanceof ApiError)) return null
  if (error.status === 422) {
    const fields = error.details.fields
    const text =
      typeof fields === 'object' && fields !== null ? (fields as Record<string, unknown>).reference : undefined
    return { reference: typeof text === 'string' && text ? text : 'The server did not accept this reference.' }
  }
  if (error.code === 'profile_busy') {
    return { form: 'A start, stop or restart is running on this profile. Try again when it has finished.' }
  }
  if (error.status === 409 && error.code === 'conflict') {
    if (error.details.limit !== undefined) {
      return { form: 'This profile is already pulling 2 images. Wait for one to finish or cancel it, then try again.' }
    }
    return {
      reference:
        'This exact reference is already being pulled. If you just cancelled it, wait a moment and try again.',
    }
  }
  if (error.status === 409 && error.code === 'invalid_state') {
    return { form: 'The profile is not running, or it does not use the Docker runtime.' }
  }
  return null
}

const errorText = 'text-xs text-destructive'

/**
 * Dialog to start an image pull. The pull itself runs as a job: the dialog
 * closes on the 202 and `onStarted` hands the job id to the section, which
 * shows progress. Nothing is sent until the reference passes the same rules as
 * the server.
 */
export function PullImageDialog({
  profile,
  open,
  onOpenChange,
  onStarted,
}: {
  profile: string
  open: boolean
  onOpenChange: (open: boolean) => void
  onStarted: (jobId: string, reference: string) => void
}) {
  const toast = useToast()
  const [reference, setReference] = useState('')
  const [submitted, setSubmitted] = useState(false)
  const [server, setServer] = useState<ServerErrors>({})
  const inputId = useId()
  const hintId = useId()
  const errorId = useId()
  const formErrorId = useId()

  const reset = () => {
    setReference('')
    setSubmitted(false)
    setServer({})
  }
  const close = () => {
    reset()
    onOpenChange(false)
  }

  const pull = useMutation({
    mutationFn: (ref: string) => api.pullImage(profile, ref),
    onSuccess: (accepted, ref) => {
      onStarted(accepted.jobId, ref)
      close()
    },
    onError: (error) => {
      const mapped = serverErrorsFrom(error)
      if (mapped) {
        setServer(mapped)
        return
      }
      const title = 'Could not start the pull'
      const description = describeDockerError(error, title)
      if (description === '') return
      const code = error instanceof ApiError ? `${error.status}:${error.code}` : 'unknown'
      toast.error({ title, description, key: `docker:${profile}:image-pull:${code}` })
    },
  })

  const value = reference.trim()
  const problem = validatePullReference(value)
  const fieldError = (submitted ? problem : undefined) ?? server.reference

  function submit(e: FormEvent) {
    e.preventDefault()
    setSubmitted(true)
    if (pull.isPending || problem) return
    setServer({})
    pull.mutate(value)
  }

  return (
    <Dialog open={open} onOpenChange={(next) => !next && close()}>
      <DialogContent className="top-[8%] max-h-[85vh] max-w-lg overflow-y-auto">
        <DialogTitle>Pull image</DialogTitle>
        <DialogDescription>
          Downloads an image into this profile. Progress shows above the images table, and you can keep working while it
          runs.
        </DialogDescription>
        <form onSubmit={submit} noValidate className="flex flex-col gap-4">
          <div className="flex flex-col gap-1.5 text-sm">
            <label htmlFor={inputId}>Image reference</label>
            <Input
              id={inputId}
              value={reference}
              onChange={(e) => {
                setServer((s) => ({ ...s, reference: undefined }))
                setReference(e.target.value)
              }}
              placeholder="postgres:18-alpine"
              autoComplete="off"
              spellCheck={false}
              autoFocus
              aria-invalid={fieldError ? true : undefined}
              aria-describedby={fieldError ? `${hintId} ${errorId}` : hintId}
            />
            <p id={hintId} className="text-xs text-muted-foreground">
              A tag or a digest is required (postgres:18-alpine, or name@sha256:…). Only public registries work, with no
              login. The VM contacts the reference&apos;s registry host as you typed it.
            </p>
            {fieldError && (
              <p id={errorId} className={errorText}>
                {fieldError}
              </p>
            )}
          </div>
          {server.form && (
            <p id={formErrorId} role="alert" className={errorText}>
              {server.form}
            </p>
          )}
          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" onClick={close}>
              Cancel
            </Button>
            <Button type="submit" disabled={pull.isPending}>
              {pull.isPending ? 'Starting…' : 'Start pull'}
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  )
}
