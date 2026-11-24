import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { describe, SELECT_CLASS } from './formStyle'
import { Message } from './formParts'
import {
  ADVANCED_SELECTS,
  cpuTypeBlocked,
  broadMountHint,
  interfaceVisible,
  MAX_DNS,
  MAX_DNS_HOSTS,
  MAX_ENV,
  MAX_MOUNTS,
  MOUNT_EXPOSURE_NOTE,
  rosettaBlocked,
  type AdvancedSelect,
  type CreateValues,
} from './validateCreate'

export interface RowHandlers {
  onDns: (index: number, value: string) => void
  addDns: () => void
  removeDns: (index: number) => void
  onHost: (index: number, field: 'name' | 'address', value: string) => void
  addHost: () => void
  removeHost: (index: number) => void
  onMount: (index: number, field: 'path' | 'writable', value: string | boolean) => void
  addMount: () => void
  removeMount: (index: number) => void
  onEnv: (index: number, field: 'key' | 'value', value: string) => void
  toggleEnvShown: (index: number) => void
  addEnv: () => void
  removeEnv: (index: number) => void
}

interface Props {
  formId: string
  values: CreateValues
  pending: boolean
  open: boolean
  onToggle: () => void
  errorFor: (key: string) => string | undefined
  edit: <K extends keyof CreateValues>(field: K, value: CreateValues[K]) => void
  rows: RowHandlers
}

const SUBHEAD = 'text-sm font-medium'

/** The collapsed-by-default Advanced disclosure of the New profile dialog. */
export function AdvancedFields({ formId, values, pending, open, onToggle, errorFor, edit, rows }: Props) {
  const panelId = `${formId}-advanced`
  const id = (key: string) => `${formId}-${key}`

  const rosetta = rosettaBlocked(values)
  const cpuBlocked = cpuTypeBlocked(values)

  const toggle = (
    key: 'sshAgent' | 'mountInotify' | 'networkAddress',
    label: string,
    hint: string,
  ) => (
    <div className="flex flex-col gap-1">
      <label className="flex items-center gap-2 text-sm font-medium">
        <input
          id={id(key)}
          type="checkbox"
          checked={values[key]}
          disabled={pending}
          aria-describedby={describe(id(key), true)}
          onChange={(e) => edit(key, e.target.checked)}
        />
        {label}
      </label>
      <Message id={id(key)} hint={hint} />
    </div>
  )

  const select = (key: AdvancedSelect, hint?: string) => {
    const error = errorFor(key)
    return (
      <div className="flex flex-col gap-1">
        <label htmlFor={id(key)} className="text-sm font-medium">
          {ADVANCED_SELECTS[key].label}
        </label>
        <select
          id={id(key)}
          className={SELECT_CLASS}
          value={values[key]}
          disabled={pending}
          aria-invalid={error ? true : undefined}
          aria-describedby={describe(id(key), !!hint, error)}
          onChange={(e) => edit(key, e.target.value)}
        >
          <option value="">Default</option>
          {ADVANCED_SELECTS[key].options.map((o) => (
            <option key={o.value} value={o.value}>
              {o.label}
            </option>
          ))}
        </select>
        <Message id={id(key)} error={error} hint={hint} />
      </div>
    )
  }

  const text = (
    key: 'hostname' | 'networkInterface' | 'cpuType',
    label: string,
    opts: { placeholder: string; hint?: string; disabled?: boolean },
  ) => {
    const error = errorFor(key)
    return (
      <div className="flex flex-col gap-1">
        <label htmlFor={id(key)} className="text-sm font-medium">
          {label}
        </label>
        <Input
          id={id(key)}
          autoComplete="off"
          autoCapitalize="none"
          spellCheck={false}
          placeholder={opts.placeholder}
          value={values[key]}
          disabled={pending || opts.disabled}
          aria-invalid={error ? true : undefined}
          aria-describedby={describe(id(key), !!opts.hint, error)}
          onChange={(e) => edit(key, e.target.value)}
        />
        <Message id={id(key)} error={error} hint={opts.hint} />
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-3">
      <button
        type="button"
        className="flex w-fit items-center gap-2 rounded-md text-sm font-medium outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
        aria-expanded={open}
        aria-controls={panelId}
        onClick={onToggle}
      >
        <span aria-hidden="true">{open ? '▾' : '▸'}</span>
        Advanced
      </button>

      <div id={panelId} hidden={!open} className="flex flex-col gap-4 border-l border-border pl-3">
        <p className="text-xs text-muted-foreground">
          Every setting here is optional. A setting you leave untouched is not sent, so Colima&apos;s default
          applies.
        </p>

        <div className="grid grid-cols-2 gap-3">
          {text('hostname', 'Hostname', {
            placeholder: 'dev-vm',
            hint: 'One DNS label: letters, digits and hyphens.',
          })}
          {select('portForwarder')}
        </div>

        <div className="grid grid-cols-2 gap-3">
          {toggle('sshAgent', 'SSH agent', 'Forwards your SSH agent into the VM.')}
          {toggle('mountInotify', 'Mount inotify', 'Passes file change events from the host to the VM.')}
        </div>

        <fieldset className="flex flex-col gap-2" aria-describedby={describe(id('dns'), true, errorFor('dns'))}>
          <legend className={SUBHEAD}>DNS servers</legend>
          <Message
            id={id('dns')}
            error={errorFor('dns')}
            hint={`IPv4 or IPv6 addresses, at most ${MAX_DNS}. Empty rows are not sent.`}
          />
          {values.dns.map((value, i) => {
            const key = `dns[${i}]`
            const error = errorFor(key)
            return (
              <div key={i} className="flex flex-col gap-1">
                <div className="flex gap-2">
                  <Input
                    id={id(key)}
                    aria-label={`DNS server ${i + 1}`}
                    autoComplete="off"
                    spellCheck={false}
                    placeholder="1.1.1.1"
                    value={value}
                    disabled={pending}
                    aria-invalid={error ? true : undefined}
                    aria-describedby={describe(id(key), false, error)}
                    onChange={(e) => rows.onDns(i, e.target.value)}
                  />
                  <Button
                    type="button"
                    variant="outline"
                    disabled={pending}
                    aria-label={`Remove DNS server ${i + 1}`}
                    onClick={() => rows.removeDns(i)}
                  >
                    Remove
                  </Button>
                </div>
                <Message id={id(key)} error={error} />
              </div>
            )
          })}
          <div>
            <Button
              id={id('dns-add')}
              type="button"
              variant="outline"
              disabled={pending || values.dns.length >= MAX_DNS}
              onClick={rows.addDns}
            >
              Add DNS server
            </Button>
          </div>
        </fieldset>

        <fieldset
          className="flex flex-col gap-2"
          aria-describedby={describe(id('dnsHosts'), true, errorFor('dnsHosts'))}
        >
          <legend className={SUBHEAD}>DNS hosts</legend>
          <Message
            id={id('dnsHosts')}
            error={errorFor('dnsHosts')}
            hint={`Custom host names the VM resolves, at most ${MAX_DNS_HOSTS}. Empty rows are not sent.`}
          />
          {values.dnsHosts.map((row, i) => {
            const nameKey = `dnsHosts[${i}].name`
            const addressKey = `dnsHosts[${i}].address`
            const nameError = errorFor(nameKey)
            const addressError = errorFor(addressKey)
            return (
              <div key={i} className="flex flex-col gap-1">
                <div className="flex gap-2">
                  <Input
                    id={id(nameKey)}
                    aria-label={`DNS host ${i + 1} name`}
                    autoComplete="off"
                    autoCapitalize="none"
                    spellCheck={false}
                    placeholder="example.com"
                    value={row.name}
                    disabled={pending}
                    aria-invalid={nameError ? true : undefined}
                    aria-describedby={describe(id(nameKey), false, nameError)}
                    onChange={(e) => rows.onHost(i, 'name', e.target.value)}
                  />
                  <Input
                    id={id(addressKey)}
                    aria-label={`DNS host ${i + 1} address`}
                    autoComplete="off"
                    spellCheck={false}
                    placeholder="192.0.2.10"
                    value={row.address}
                    disabled={pending}
                    aria-invalid={addressError ? true : undefined}
                    aria-describedby={describe(id(addressKey), false, addressError)}
                    onChange={(e) => rows.onHost(i, 'address', e.target.value)}
                  />
                  <Button
                    type="button"
                    variant="outline"
                    disabled={pending}
                    aria-label={`Remove DNS host ${i + 1}`}
                    onClick={() => rows.removeHost(i)}
                  >
                    Remove
                  </Button>
                </div>
                <Message id={id(nameKey)} error={nameError} />
                <Message id={id(addressKey)} error={addressError} />
              </div>
            )
          })}
          <div>
            <Button
              id={id('dnsHosts-add')}
              type="button"
              variant="outline"
              disabled={pending || values.dnsHosts.length >= MAX_DNS_HOSTS}
              onClick={rows.addHost}
            >
              Add DNS host
            </Button>
          </div>
        </fieldset>

        <div className="grid grid-cols-2 gap-3">
          {toggle('networkAddress', 'Network address', 'Gives the VM an IP address reachable from the host.')}
          {select(
            'networkMode',
            interfaceVisible(values) ? undefined : 'Choose bridged to pick a host network interface.',
          )}
          {interfaceVisible(values) &&
            text('networkInterface', 'Network interface', {
              placeholder: 'en0',
              hint: 'Host interface for bridged mode.',
            })}
        </div>

        <div className="grid grid-cols-2 gap-3">
          <div className="flex flex-col gap-1">
            <label className="flex items-center gap-2 text-sm font-medium">
              <input
                id={id('vzRosetta')}
                type="checkbox"
                checked={values.vzRosetta && !rosetta}
                disabled={pending || !!rosetta}
                aria-invalid={errorFor('vzRosetta') ? true : undefined}
                aria-describedby={describe(id('vzRosetta'), true, errorFor('vzRosetta'))}
                onChange={(e) => edit('vzRosetta', e.target.checked)}
              />
              Rosetta
            </label>
            <Message
              id={id('vzRosetta')}
              error={errorFor('vzRosetta')}
              hint={
                rosetta ||
                'Runs amd64 programs in the VM. Needs VZ and aarch64; on an Intel Mac, set Architecture to aarch64.'
              }
            />
          </div>
          {text('cpuType', 'CPU type', {
            placeholder: 'max',
            disabled: !!cpuBlocked,
            hint: cpuBlocked || 'One CPU model name, for example max or cortex-a72.',
          })}
        </div>
        <fieldset
          className="flex flex-col gap-2"
          aria-describedby={[id('mounts-hint'), id('mounts-default'), errorFor('mounts') ? id('mounts-error') : null]
            .filter(Boolean)
            .join(' ')}
        >
          <legend className={SUBHEAD}>Mounts</legend>
          <p id={id('mounts-hint')} className="text-xs font-medium text-foreground">
            {MOUNT_EXPOSURE_NOTE}
          </p>
          <p id={id('mounts-default')} className="text-xs text-muted-foreground">
            {`Folders must exist on this machine, at most ${MAX_MOUNTS}. A writable mount lets containers change files on your Mac. Listing mounts here may replace Colima's default mount of your home folder. Empty rows are not sent.`}
          </p>
          {errorFor('mounts') && (
            <p id={id('mounts-error')} className="text-xs text-destructive">
              {errorFor('mounts')}
            </p>
          )}
          {values.mounts.map((row, i) => {
            const key = `mounts[${i}].path`
            const error = errorFor(key)
            const hint = broadMountHint(row)
            return (
              <div key={i} className="flex flex-col gap-1">
                <div className="flex items-center gap-2">
                  <Input
                    id={id(key)}
                    aria-label={`Mount ${i + 1} path`}
                    autoComplete="off"
                    autoCapitalize="none"
                    spellCheck={false}
                    placeholder="/Users/me/src"
                    value={row.path}
                    disabled={pending}
                    aria-invalid={error ? true : undefined}
                    aria-describedby={describe(id(key), !!hint, error)}
                    onChange={(e) => rows.onMount(i, 'path', e.target.value)}
                  />
                  <label className="flex shrink-0 items-center gap-1 text-sm">
                    <input
                      type="checkbox"
                      aria-label={`Mount ${i + 1} writable`}
                      checked={row.writable}
                      disabled={pending}
                      onChange={(e) => rows.onMount(i, 'writable', e.target.checked)}
                    />
                    Writable
                  </label>
                  <Button
                    type="button"
                    variant="outline"
                    disabled={pending}
                    aria-label={`Remove mount ${i + 1}`}
                    onClick={() => rows.removeMount(i)}
                  >
                    Remove
                  </Button>
                </div>
                <Message id={id(key)} error={error} hint={hint} />
              </div>
            )
          })}
          <div>
            <Button
              id={id('mounts-add')}
              type="button"
              variant="outline"
              disabled={pending || values.mounts.length >= MAX_MOUNTS}
              onClick={rows.addMount}
            >
              Add mount
            </Button>
          </div>
        </fieldset>

        <fieldset className="flex flex-col gap-2" aria-describedby={describe(id('env'), true, errorFor('env'))}>
          <legend className={SUBHEAD}>Environment variables</legend>
          <Message
            id={id('env')}
            error={errorFor('env')}
            hint={`Set in the VM, at most ${MAX_ENV}. Values are hidden by default and names are case sensitive. Empty rows are not sent.`}
          />
          {values.env.map((row, i) => {
            const keyKey = `env[${i}].key`
            const valueKey = `env[${i}].value`
            const keyError = errorFor(keyKey)
            const valueError = errorFor(valueKey)
            const label = row.key.trim() === '' ? `variable ${i + 1}` : row.key.trim()
            return (
              <div key={i} className="flex flex-col gap-1">
                <div className="flex gap-2">
                  <Input
                    id={id(keyKey)}
                    aria-label={`Environment variable ${i + 1} name`}
                    autoComplete="off"
                    autoCapitalize="none"
                    spellCheck={false}
                    placeholder="HTTP_PROXY"
                    value={row.key}
                    disabled={pending}
                    aria-invalid={keyError ? true : undefined}
                    aria-describedby={describe(id(keyKey), false, keyError)}
                    onChange={(e) => rows.onEnv(i, 'key', e.target.value)}
                  />
                  <Input
                    id={id(valueKey)}
                    type={row.shown ? 'text' : 'password'}
                    aria-label={`Environment variable ${i + 1} value`}
                    autoComplete="new-password"
                    autoCapitalize="none"
                    spellCheck={false}
                    data-1p-ignore
                    data-lpignore="true"
                    value={row.value}
                    disabled={pending}
                    aria-invalid={valueError ? true : undefined}
                    aria-describedby={describe(id(valueKey), false, valueError)}
                    onChange={(e) => rows.onEnv(i, 'value', e.target.value)}
                  />
                  <Button
                    type="button"
                    variant="outline"
                    disabled={pending}
                    aria-label={`Show value of ${label}`}
                    aria-pressed={row.shown}
                    onClick={() => rows.toggleEnvShown(i)}
                  >
                    {row.shown ? 'Hide' : 'Show'}
                  </Button>
                  <Button
                    type="button"
                    variant="outline"
                    disabled={pending}
                    aria-label={`Remove environment variable ${i + 1}`}
                    onClick={() => rows.removeEnv(i)}
                  >
                    Remove
                  </Button>
                </div>
                <Message id={id(keyKey)} error={keyError} />
                <Message id={id(valueKey)} error={valueError} />
              </div>
            )
          })}
          <div>
            <Button
              id={id('env-add')}
              type="button"
              variant="outline"
              disabled={pending || values.env.length >= MAX_ENV}
              onClick={rows.addEnv}
            >
              Add variable
            </Button>
          </div>
        </fieldset>
      </div>
    </div>
  )
}
