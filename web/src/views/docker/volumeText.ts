import { formatBytes } from '@/lib/format'
import type { VolumePruneResult } from '@/lib/api/types'

export const plural = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`

/** The toast title of a finished prune, honest about figures the server could not read. */
export function pruneTitle(r: VolumePruneResult): string {
  if (!r.resultComplete || r.removedCount === null) {
    return 'Prune ran and some volumes were removed. The exact result is unknown, refresh the list to see what is left.'
  }
  if (r.removedCount === 0) return 'No unused volumes were removed.'
  const parts = [`Removed ${plural(r.removedCount, 'volume', 'volumes')}`]
  if (r.reclaimedBytes !== null) parts.push(`freed ${formatBytes(r.reclaimedBytes)}`)
  let title = parts.join(', ')
  if (r.truncated) title += ' (not every name is listed)'
  return title
}
