import type { ConfigUpdate, ProfileConfig } from '@/lib/api/types'

export type FieldName = 'cpu' | 'memoryGiB' | 'diskGiB'
export const FIELD_NAMES: readonly FieldName[] = ['cpu', 'memoryGiB', 'diskGiB']

export type ConfigValues = Record<FieldName, string>
export type FieldErrors = Partial<Record<FieldName, string>>

export const FIELD_LABELS: Record<FieldName, string> = {
  cpu: 'CPU',
  memoryGiB: 'Memory',
  diskGiB: 'Disk',
}

export function toValues(config: ProfileConfig): ConfigValues {
  return {
    cpu: String(config.cpu),
    memoryGiB: String(config.memoryGiB),
    diskGiB: String(config.diskGiB),
  }
}

function parse(raw: string): number {
  const s = raw.trim()
  return s === '' ? NaN : Number(s)
}

/** Fields whose numeric value differs from the loaded config. Unparseable input counts as changed. */
export function changedFields(values: ConfigValues, loaded: ProfileConfig): FieldName[] {
  return FIELD_NAMES.filter((f) => {
    const n = parse(values[f])
    return Number.isNaN(n) || n !== loaded[f]
  })
}

/**
 * Client-side checks for what the browser can know. The server stays the
 * validator (it also knows the host core count); its messages override these.
 */
export function validateConfig(values: ConfigValues, loaded: ProfileConfig): FieldErrors {
  const errors: FieldErrors = {}

  const cpu = parse(values.cpu)
  if (!Number.isInteger(cpu) || cpu < 1) errors.cpu = 'CPU must be a whole number, at least 1.'

  const mem = parse(values.memoryGiB)
  if (!Number.isFinite(mem) || mem < 1 || mem > 256) {
    errors.memoryGiB = 'Memory must be a number between 1 and 256 GiB.'
  }

  const disk = parse(values.diskGiB)
  if (!Number.isInteger(disk) || disk < 1 || disk > 8192) {
    errors.diskGiB = 'Disk must be a whole number between 1 and 8192 GiB.'
  } else if (disk < loaded.diskGiB) {
    errors.diskGiB = `Disk can only grow. The current size is ${loaded.diskGiB} GiB.`
  }

  return errors
}

/** The PUT body: only the changed fields. */
export function buildUpdate(values: ConfigValues, loaded: ProfileConfig): ConfigUpdate {
  const update: ConfigUpdate = {}
  for (const f of changedFields(values, loaded)) update[f] = parse(values[f])
  return update
}
