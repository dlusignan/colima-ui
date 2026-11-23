import type { ContainerPort } from '@/lib/api/types'

export type ContainerHealth = 'running' | 'paused' | 'exited' | 'crashed' | 'other'

/**
 * Classifies a container from Docker's state and status text. `paused` comes from state `paused`
 * (Docker's status text then reads `Up 5 minutes (Paused)`). `crashed` means `Exited (N)` with N not 0.
 * `Created`, `Restarting (1) ...` and empty text are `other`.
 */
export function containerHealth(status: string, state = ''): ContainerHealth {
  const text = status.trim()
  if (state === 'paused' || /\(Paused\)/i.test(text)) return 'paused'
  if (/^Up\b/.test(text)) return 'running'
  const exited = /^Exited\b(?:\s*\((-?\d+)\))?/.exec(text)
  if (exited) return exited[1] !== undefined && Number(exited[1]) !== 0 ? 'crashed' : 'exited'
  return 'other'
}

/**
 * One label per port, deduplicated (Docker lists a published port once for 0.0.0.0 and once for ::):
 * `8080:80/tcp` published, `80/tcp` exposed only.
 */
export function formatPorts(ports: ContainerPort[]): string[] {
  const labels = ports.map((p) => {
    const target = `${p.containerPort}/${p.protocol}`
    return p.hostPort ? `${p.hostPort}:${target}` : target
  })
  return [...new Set(labels)]
}
