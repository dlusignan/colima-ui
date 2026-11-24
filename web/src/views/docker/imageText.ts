import { formatBytes } from '@/lib/format'
import type { ImagePruneResult, ImageRemoveResult } from '@/lib/api/types'
import { plural } from './volumeText'

interface Entries {
  untagged: string[]
  deleted: string[]
  removedCount: number | null
  truncated: boolean
}

/** What was removed, from the lists or, when they are cut short, from the count. */
function summary(r: Entries): string {
  if (r.truncated && r.removedCount !== null) {
    return `Removed ${plural(r.removedCount, 'item', 'items')} (images and tags)`
  }
  if (r.deleted.length > 0) {
    const images = `Removed ${plural(r.deleted.length, 'image', 'images')}`
    return r.untagged.length > 0
      ? `${images}, untagged ${plural(r.untagged.length, 'reference', 'references')}`
      : images
  }
  return `Untagged ${plural(r.untagged.length, 'reference', 'references')}`
}

/** The toast title of a finished removal, honest about a result the server could not read. */
export function removeTitle(r: ImageRemoveResult): string {
  if (!r.resultComplete || r.removedCount === null) {
    return 'The removal ran. Docker answered in a way the server could not read, so the details are unknown. Refresh the list to see what is left.'
  }
  if (r.removedCount === 0) return 'Nothing was removed.'
  return r.truncated ? `${summary(r)} (not every entry is listed)` : summary(r)
}

/** The toast title of a finished prune, honest about figures the server could not read. */
export function imagePruneTitle(r: ImagePruneResult): string {
  if (!r.resultComplete || r.removedCount === null) {
    return 'Prune ran and some images were removed. The exact result is unknown, refresh the list to see what is left.'
  }
  if (r.removedCount === 0) return r.all ? 'No unused images were removed.' : 'No dangling images were removed.'
  let title = summary(r)
  if (r.reclaimedBytes !== null) title += `, freed ${formatBytes(r.reclaimedBytes)}`
  if (r.truncated) title += ' (not every entry is listed)'
  return title
}

const list = (items: unknown): string[] =>
  Array.isArray(items) ? items.filter((i): i is string => typeof i === 'string') : []

/** The references and ids a failed removal reported as already gone (docs/api.md 3.28, partial failure). */
export function partialRemoval(details: Record<string, unknown>): { untagged: string[]; deleted: string[] } | null {
  const untagged = list(details.untagged)
  const deleted = list(details.deleted)
  return untagged.length + deleted.length > 0 ? { untagged, deleted } : null
}

/** The container count a 409 reports, when it carries one. */
export function containersInUse(details: Record<string, unknown>): number | null {
  const n = details.containers
  return typeof n === 'number' && Number.isFinite(n) && n > 0 ? n : null
}
