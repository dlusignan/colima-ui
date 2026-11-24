import type { DockerNetwork, NetworkPruneResult } from '@/lib/api/types'
import { isBuiltInNetwork } from './networkRules'
import { plural } from './volumeText'

/** Why a network cannot be removed from here, or undefined when it can. */
export function networkRemoveReason(n: DockerNetwork): string | undefined {
  if (isBuiltInNetwork(n.name)) return 'Built into Docker'
  if (n.containers > 0) return `${plural(n.containers, 'container', 'containers')} attached`
  return undefined
}

/** The toast title of a finished prune, honest about a result the server could not read. */
export function networkPruneTitle(r: NetworkPruneResult): string {
  if (!r.resultComplete || r.removedCount === null) {
    return 'Prune ran and some networks were removed. The exact result is unknown, refresh the list to see what is left.'
  }
  if (r.removedCount === 0) return 'No unused networks were removed.'
  let title = `Removed ${plural(r.removedCount, 'network', 'networks')}`
  if (r.truncated) title += ' (not every name is listed)'
  return title
}
