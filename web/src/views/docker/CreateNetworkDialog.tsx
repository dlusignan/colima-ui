import { useId, useState, type FormEvent } from 'react'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { ApiError } from '@/lib/api/client'
import { api } from '@/lib/api/endpoints'
import { useDockerMutation } from './useDockerMutation'
import {
  buildCreateRequest,
  hasErrors,
  SUBNET_HINT,
  validateNetworkForm,
} from './networkRules'

interface ServerErrors {
  name?: string
  subnet?: string
}

/** Maps a 409 or 422 of the create route to the field it belongs to. Other errors are not handled here. */
function serverErrorsFrom(error: unknown, withSubnet: boolean): ServerErrors | null {
  if (!(error instanceof ApiError)) return null
  if (error.status === 409 && error.code === 'conflict') {
    // 3.22: a 409 is a duplicate name, or an address range that overlaps another Docker network.
    return {
      name: withSubnet
        ? 'A network with this name already exists, or the subnet overlaps another Docker network.'
        : 'A network with this name already exists.',
    }
  }
  if (error.status === 422) {
    const fields = error.details.fields
    const out: ServerErrors = {}
    if (typeof fields === 'object' && fields !== null) {
      const f = fields as Record<string, unknown>
      if (typeof f.name === 'string' && f.name) out.name = f.name
      if (typeof f.subnet === 'string' && f.subnet) out.subnet = f.subnet
    }
    if (!out.name && !out.subnet) out.name = 'Docker rejected this request. Check the name and the subnet.'
    return out
  }
  return null
}

const errorText = 'text-xs text-destructive'

/** Dialog to create a bridge network: a name, an internal flag and an optional subnet. Nothing is sent until the form is valid. */
export function CreateNetworkDialog({
  profile,
  open,
  onOpenChange,
}: {
  profile: string
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const [server, setServer] = useState<ServerErrors>({})
  const mutation = useDockerMutation({
    profile,
    section: 'networks',
    action: 'network-create',
    mutationFn: (body: ReturnType<typeof buildCreateRequest>) => api.createNetwork(profile, body),
    successTitle: (_d, n) => `Created network ${n.name}`,
    errorTitle: (n) => `Could not create network ${n.name}`,
    onSuccess: () => onOpenChange(false),
    handleError: (error, body) => {
      const mapped = serverErrorsFrom(error, body.subnet !== undefined)
      if (!mapped) return false
      setServer(mapped)
      return true
    },
  })
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="top-[8%] max-h-[85vh] max-w-lg overflow-y-auto">
        <DialogTitle>Create network</DialogTitle>
        <DialogDescription>
          Creates a bridge network in {profile}. Other drivers and advanced address options are not available here.
        </DialogDescription>
        <CreateNetworkForm
          server={server}
          clearServer={(field) => setServer((s) => ({ ...s, [field]: undefined }))}
          pending={mutation.isPending}
          onCancel={() => onOpenChange(false)}
          onSubmit={(body) => {
            setServer({})
            mutation.run(body)
          }}
        />
      </DialogContent>
    </Dialog>
  )
}

function CreateNetworkForm({
  server,
  clearServer,
  pending,
  onCancel,
  onSubmit,
}: {
  server: ServerErrors
  clearServer: (field: keyof ServerErrors) => void
  pending: boolean
  onCancel: () => void
  onSubmit: (body: ReturnType<typeof buildCreateRequest>) => void
}) {
  const [name, setName] = useState('')
  const [internal, setInternal] = useState(false)
  const [subnet, setSubnet] = useState('')
  const [submitted, setSubmitted] = useState(false)
  const nameId = useId()
  const nameErrorId = useId()
  const driverId = useId()
  const internalId = useId()
  const internalHintId = useId()
  const subnetId = useId()
  const subnetHintId = useId()
  const subnetErrorId = useId()
  const errors = validateNetworkForm(name, subnet)
  const shown = submitted ? errors : {}
  const nameError = shown.name ?? server.name
  const subnetError = shown.subnet ?? server.subnet

  function submit(e: FormEvent) {
    e.preventDefault()
    setSubmitted(true)
    if (pending || hasErrors(errors)) return
    onSubmit(buildCreateRequest(name, internal, subnet))
  }

  return (
    <form onSubmit={submit} noValidate className="flex flex-col gap-4">
      <div className="flex flex-col gap-1.5 text-sm">
        <label htmlFor={nameId}>Name</label>
        <Input
          id={nameId}
          value={name}
          onChange={(e) => {
            clearServer('name')
            setName(e.target.value)
          }}
          autoComplete="off"
          spellCheck={false}
          autoFocus
          aria-invalid={nameError ? true : undefined}
          aria-describedby={nameError ? nameErrorId : undefined}
        />
        {nameError && (
          <p id={nameErrorId} className={errorText}>
            {nameError}
          </p>
        )}
      </div>

      <div className="flex flex-col gap-1.5 text-sm">
        <label htmlFor={driverId}>Driver</label>
        <Input id={driverId} value="bridge" readOnly />
      </div>

      <div className="flex flex-col gap-1">
        <label htmlFor={internalId} className="flex items-center gap-2 text-sm">
          <input
            id={internalId}
            type="checkbox"
            checked={internal}
            onChange={(e) => setInternal(e.target.checked)}
            aria-describedby={internalHintId}
          />
          Internal (no outside access)
        </label>
        <p id={internalHintId} className="text-xs text-muted-foreground">
          Containers on an internal network can reach each other but not the outside.
        </p>
      </div>

      <div className="flex flex-col gap-1.5 text-sm">
        <label htmlFor={subnetId}>Subnet (optional)</label>
        <Input
          id={subnetId}
          value={subnet}
          onChange={(e) => {
            clearServer('subnet')
            setSubnet(e.target.value)
          }}
          placeholder="172.28.0.0/16"
          autoComplete="off"
          spellCheck={false}
          aria-invalid={subnetError ? true : undefined}
          aria-describedby={subnetError ? `${subnetHintId} ${subnetErrorId}` : subnetHintId}
        />
        <p id={subnetHintId} className="text-xs text-muted-foreground">
          {SUBNET_HINT}
        </p>
        {subnetError && (
          <p id={subnetErrorId} className={errorText}>
            {subnetError}
          </p>
        )}
      </div>

      <div className="flex justify-end gap-2">
        <Button type="button" variant="outline" onClick={onCancel}>
          Cancel
        </Button>
        <Button type="submit" disabled={pending}>
          {pending ? 'Creating…' : 'Create network'}
        </Button>
      </div>
    </form>
  )
}
