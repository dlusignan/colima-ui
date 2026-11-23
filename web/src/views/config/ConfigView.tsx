import { useEffect, useId, useState, type FormEvent } from 'react'
import { Link, useBlocker, useParams } from 'react-router-dom'
import { ConfirmDialog } from '@/components/ConfirmDialog'
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
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { ApiError } from '@/lib/api/client'
import { useConfig, useSaveConfig } from '@/lib/api/useConfig'
import { useProfiles } from '@/lib/api/useProfiles'
import type { ProfileConfig } from '@/lib/api/types'
import { useProfileAction } from '@/lib/jobs/useProfileAction'
import { logPath } from '@/views/log/logPath'
import { describeConfigError, fieldErrorsFrom } from './describeConfigError'
import {
  buildUpdate,
  changedFields,
  FIELD_LABELS,
  FIELD_NAMES,
  toValues,
  validateConfig,
  type ConfigValues,
  type FieldErrors,
  type FieldName,
} from './validateConfig'

const FIELD_META: Record<FieldName, { unit: string; step: string; hint: (c: ProfileConfig) => string }> = {
  cpu: { unit: 'cores', step: '1', hint: (c) => `Current: ${c.cpu}` },
  memoryGiB: { unit: 'GiB', step: '0.5', hint: (c) => `Current: ${c.memoryGiB} GiB` },
  diskGiB: { unit: 'GiB', step: '1', hint: (c) => `Current: ${c.diskGiB} GiB. Can only grow.` },
}

export function ConfigView() {
  const { name = '' } = useParams()
  const config = useConfig(name)
  const profiles = useProfiles()

  if (config.isPending) return <p className="text-sm text-muted-foreground">Loading configuration...</p>

  if (config.isError) {
    const err = config.error
    if (err instanceof ApiError && err.status === 404) {
      const known = profiles.data?.profiles.some((p) => p.name === name)
      return (
        <div role="status" className="flex flex-col gap-2 text-sm">
          {known || !profiles.data ? (
            <p>
              No configuration file yet. Start the profile once so Colima creates it.{' '}
              <Link className="underline" to={`/profiles/${encodeURIComponent(name)}`}>
                Back to the profile
              </Link>
            </p>
          ) : (
            <p>The profile {name} does not exist.</p>
          )}
        </div>
      )
    }
    return (
      <p role="alert" className="text-sm text-destructive">
        {describeConfigError(err) || 'Could not load the configuration.'}
      </p>
    )
  }

  return <ConfigForm name={name} loaded={config.data} />
}

function ConfigForm({ name, loaded }: { name: string; loaded: ProfileConfig }) {
  const formId = useId()
  const profiles = useProfiles()
  const save = useSaveConfig(name)
  const restart = useProfileAction(name)

  const [values, setValues] = useState<ConfigValues>(() => toValues(loaded))
  const [clientErrors, setClientErrors] = useState<FieldErrors>({})
  const [serverErrors, setServerErrors] = useState<FieldErrors>({})
  const [summary, setSummary] = useState('')
  const [saved, setSaved] = useState<string[] | null>(null)
  const [focusRequest, setFocusRequest] = useState<{ field: FieldName; n: number } | null>(null)

  const profile = profiles.data?.profiles.find((p) => p.name === name)
  const busy = Boolean(profile?.activeJobId) || restart.isPending
  const changed = changedFields(values, loaded)
  const dirty = changed.length > 0

  const blocker = useBlocker(dirty)

  useEffect(() => {
    if (!dirty) return
    const onBeforeUnload = (e: BeforeUnloadEvent) => {
      e.preventDefault()
    }
    window.addEventListener('beforeunload', onBeforeUnload)
    return () => window.removeEventListener('beforeunload', onBeforeUnload)
  }, [dirty])

  useEffect(() => {
    if (focusRequest) document.getElementById(`${formId}-${focusRequest.field}`)?.focus()
  }, [focusRequest, formId])

  function edit(field: FieldName, value: string) {
    setValues((v) => ({ ...v, [field]: value }))
    setClientErrors((e) => ({ ...e, [field]: undefined }))
    setServerErrors((e) => ({ ...e, [field]: undefined }))
  }

  function firstInvalid(errors: FieldErrors): FieldName | undefined {
    return FIELD_NAMES.find((f) => errors[f])
  }

  function onSubmit(e: FormEvent) {
    e.preventDefault()
    if (busy || save.isPending || !dirty) return
    setSummary('')
    setSaved(null)
    const errors = validateConfig(values, loaded)
    setClientErrors(errors)
    const bad = firstInvalid(errors)
    if (bad) {
      setServerErrors({})
      setSummary('Fix the marked fields and save again.')
      setFocusRequest((r) => ({ field: bad, n: (r?.n ?? 0) + 1 }))
      return
    }
    save.mutate(buildUpdate(values, loaded), {
      onSuccess: (result) => {
        setServerErrors({})
        setValues(toValues(result.config))
        setSaved(result.restartRequired)
      },
      onError: (error) => {
        const fields = fieldErrorsFrom(error)
        setServerErrors(fields)
        setSummary(describeConfigError(error))
        const bad = firstInvalid(fields)
        if (bad) setFocusRequest((r) => ({ field: bad, n: (r?.n ?? 0) + 1 }))
      },
    })
  }

  const errorFor = (f: FieldName) => serverErrors[f] ?? clientErrors[f]
  const disabled = busy || save.isPending

  return (
    <div className="flex max-w-xl flex-col gap-4">
      <form onSubmit={onSubmit} noValidate aria-label="Configuration" className="flex flex-col gap-4">
        {FIELD_NAMES.map((f) => {
          const id = `${formId}-${f}`
          const error = errorFor(f)
          const isChanged = changed.includes(f)
          return (
            <div key={f} className="flex flex-col gap-1">
              <div className="flex items-baseline gap-2">
                <label htmlFor={id} className="text-sm font-medium">
                  {FIELD_LABELS[f]} ({FIELD_META[f].unit})
                </label>
                {isChanged && <span className="text-xs text-primary">changed</span>}
              </div>
              <Input
                id={id}
                type="number"
                step={FIELD_META[f].step}
                inputMode="decimal"
                value={values[f]}
                disabled={disabled}
                aria-invalid={error ? true : undefined}
                aria-describedby={error ? `${id}-hint ${id}-error` : `${id}-hint`}
                onChange={(e) => edit(f, e.target.value)}
              />
              <p id={`${id}-hint`} className="text-xs text-muted-foreground">
                {FIELD_META[f].hint(loaded)}
              </p>
              {error && (
                <p id={`${id}-error`} className="text-xs text-destructive">
                  {error}
                </p>
              )}
            </div>
          )
        })}

        <div role="alert" className="text-sm text-destructive">
          {summary}
        </div>

        {busy && (
          <p role="status" className="text-sm text-muted-foreground">
            A job is running on this profile. Editing is disabled until it finishes.
          </p>
        )}

        <div className="flex gap-2">
          <Button type="submit" disabled={disabled || !dirty}>
            {save.isPending ? 'Saving...' : 'Save'}
          </Button>
          <Button
            type="button"
            variant="outline"
            disabled={disabled || !dirty}
            onClick={() => {
              setValues(toValues(loaded))
              setClientErrors({})
              setServerErrors({})
              setSummary('')
            }}
          >
            Reset
          </Button>
        </div>
      </form>

      <div role="status">
        {saved !== null &&
          (saved.length === 0 ? (
            <p className="text-sm">Saved. Applies on next start.</p>
          ) : (
            <div className="flex flex-col gap-2 text-sm">
              <p>
                Saved. Restart required for:{' '}
                {saved.map((f) => FIELD_LABELS[f as FieldName] ?? f).join(', ')}.
              </p>
              {restart.isSuccess ? (
                <Link className="underline" to={logPath(name, restart.data.jobId)}>
                  View the restart log
                </Link>
              ) : (
                <ConfirmDialog
                  trigger={<Button type="button" disabled={busy}>Restart now</Button>}
                  title={`Restart ${name}?`}
                  description="The VM and its containers stop briefly while it restarts."
                  confirmLabel="Restart"
                  onConfirm={() => restart.mutate({ action: 'restart' })}
                />
              )}
              {restart.isError && (
                <p className="text-destructive">
                  {restart.error instanceof ApiError && restart.error.status === 409
                    ? 'The profile was busy. Try again.'
                    : 'Could not start the restart.'}
                </p>
              )}
            </div>
          ))}
      </div>

      <AlertDialog open={blocker.state === 'blocked'}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Discard unsaved changes?</AlertDialogTitle>
            <AlertDialogDescription>
              You changed the configuration but did not save it.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel onClick={() => blocker.reset?.()}>Stay</AlertDialogCancel>
            <AlertDialogAction variant="destructive" onClick={() => blocker.proceed?.()}>
              Leave
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
