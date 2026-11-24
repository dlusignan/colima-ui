import type { CreateVolumeRequest } from '@/lib/api/types'

/** Limits of docs/api.md 1.3 and 3.18, checked in the browser before anything is sent. */
export const MAX_VOLUME_NAME = 128
export const MAX_LABELS = 16
export const MAX_LABEL_KEY = 63
export const MAX_LABEL_VALUE_BYTES = 256

const NAME_PATTERN = /^[a-zA-Z0-9][a-zA-Z0-9_.-]*$/
const KEY_PATTERN = /^[a-zA-Z0-9][a-zA-Z0-9_.-]*$/
const RESERVED_PREFIXES = ['com.docker.', 'io.docker.', 'org.dockerproject.']
// Control, format and lone surrogate characters.
const FORBIDDEN_VALUE_CHARS = /[\p{Cc}\p{Cf}\p{Cs}]/u

export interface LabelRow {
  /** Stable React key, not sent. */
  id: number
  key: string
  value: string
}

export interface VolumeFormErrors {
  name?: string
  /** One message per row id. */
  rows: Record<number, { key?: string; value?: string }>
  labels?: string
}

export const isBlankRow = (r: LabelRow) => r.key === '' && r.value === ''

export function validateVolumeName(name: string): string | undefined {
  if (name === '') return 'Enter a name.'
  if (name.length > MAX_VOLUME_NAME) return `Use at most ${MAX_VOLUME_NAME} characters.`
  if (!NAME_PATTERN.test(name)) {
    return 'Use letters, digits, underscore, dot and hyphen, starting with a letter or digit.'
  }
  return undefined
}

export function validateVolumeForm(name: string, rows: LabelRow[]): VolumeFormErrors {
  const errors: VolumeFormErrors = { rows: {} }
  const nameError = validateVolumeName(name)
  if (nameError) errors.name = nameError
  const used = rows.filter((r) => !isBlankRow(r))
  if (used.length > MAX_LABELS) errors.labels = `Use at most ${MAX_LABELS} labels.`
  const seen = new Map<string, number>()
  for (const row of used) {
    const out: { key?: string; value?: string } = {}
    const lower = row.key.toLowerCase()
    if (row.key === '') {
      out.key = 'Enter a key.'
    } else if (row.key.length > MAX_LABEL_KEY) {
      out.key = `Use at most ${MAX_LABEL_KEY} characters.`
    } else if (!KEY_PATTERN.test(row.key)) {
      out.key = 'Use letters, digits, underscore, dot and hyphen, starting with a letter or digit.'
    } else if (RESERVED_PREFIXES.some((p) => lower.startsWith(p))) {
      out.key = 'This prefix is reserved by Docker.'
    } else if (seen.has(lower)) {
      out.key = 'This key is already used (keys are not case sensitive).'
    }
    if (row.key !== '' && !seen.has(lower)) seen.set(lower, row.id)
    if (FORBIDDEN_VALUE_CHARS.test(row.value)) {
      out.value = 'Remove control and invisible formatting characters.'
    } else if (new TextEncoder().encode(row.value).length > MAX_LABEL_VALUE_BYTES) {
      out.value = `Use at most ${MAX_LABEL_VALUE_BYTES} bytes.`
    }
    if (out.key || out.value) errors.rows[row.id] = out
  }
  return errors
}

export function hasErrors(e: VolumeFormErrors): boolean {
  return Boolean(e.name) || Boolean(e.labels) || Object.keys(e.rows).length > 0
}

/** `{ name }`, or `{ name, labels }` when at least one row is filled in. Blank rows are dropped. */
export function buildCreateRequest(name: string, rows: LabelRow[]): CreateVolumeRequest {
  const used = rows.filter((r) => !isBlankRow(r))
  if (used.length === 0) return { name }
  const labels: Record<string, string> = {}
  for (const r of used) labels[r.key] = r.value
  return { name, labels }
}
