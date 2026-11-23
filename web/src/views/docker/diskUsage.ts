import type { EngineDiskUsage } from '@/lib/api/types'

export interface DiskSegment {
  key: 'images' | 'containers' | 'volumes' | 'buildCache'
  label: string
  bytes: number
  /** Share of the total, 0 to 100. */
  percent: number
}

/** Segments of the disk usage bar in a fixed order. All zeros (or invalid values) give zero widths, never NaN. */
export function diskSegments(usage: EngineDiskUsage): { segments: DiskSegment[]; total: number } {
  const clean = (n: number) => (Number.isFinite(n) && n > 0 ? n : 0)
  const parts: Array<[DiskSegment['key'], string, number]> = [
    ['images', 'Images', clean(usage.imagesBytes)],
    ['containers', 'Containers', clean(usage.containersBytes)],
    ['volumes', 'Volumes', clean(usage.volumesBytes)],
    ['buildCache', 'Build cache', clean(usage.buildCacheBytes)],
  ]
  const total = parts.reduce((sum, p) => sum + p[2], 0)
  const segments = parts.map(([key, label, bytes]) => ({
    key,
    label,
    bytes,
    percent: total === 0 ? 0 : (bytes / total) * 100,
  }))
  return { segments, total }
}
