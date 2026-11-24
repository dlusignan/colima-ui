import { ConfirmDialog } from '@/components/ConfirmDialog'
import { Button } from '@/components/ui/button'
import type { KubernetesAction } from '@/lib/api/types'

interface KubernetesActionsProps {
  profile: string
  /** The VM is running; every action needs it. */
  running: boolean
  /** A job is running, or a request is in flight, for this profile. */
  busy: boolean
  onAction: (action: KubernetesAction) => void
}

const DESTROY: Record<'kubernetes-reset' | 'kubernetes-delete', { label: string; description: (n: string) => string }> = {
  'kubernetes-reset': {
    label: 'Reset',
    description: (n) =>
      `Reset the Kubernetes cluster of ${n}? All workloads and cluster state are destroyed and the cluster starts again empty. If the reset is cancelled part-way the cluster may be left half done; run it again to finish.`,
  },
  'kubernetes-delete': {
    label: 'Delete',
    description: (n) =>
      `Delete the Kubernetes cluster of ${n}? All workloads and cluster state are destroyed and the cluster is removed. If the delete is cancelled part-way the cluster may be left half done; run it again to finish.`,
  },
}

/**
 * Start, Stop, Reset and Delete for the Kubernetes cluster of one profile. The
 * caller renders it only when Kubernetes is enabled. Reset and Delete confirm
 * first and say that workloads and cluster state are destroyed.
 */
export function KubernetesActions({ profile, running, busy, onAction }: KubernetesActionsProps) {
  const off = busy || !running
  const plain = (action: 'kubernetes-start' | 'kubernetes-stop', label: string) => (
    <Button
      size="xs"
      variant="outline"
      focusableWhenDisabled
      disabled={off}
      aria-label={`${label} Kubernetes on ${profile}`}
      onClick={() => onAction(action)}
    >
      {label}
    </Button>
  )
  const destructive = (action: 'kubernetes-reset' | 'kubernetes-delete') => {
    const { label, description } = DESTROY[action]
    const button = (
      <Button
        size="xs"
        variant="destructive"
        focusableWhenDisabled
        disabled={off}
        aria-label={`${label} Kubernetes on ${profile}`}
      >
        {label}
      </Button>
    )
    // A disabled trigger must not open the dialog, so it renders on its own.
    if (off) return button
    return (
      <ConfirmDialog
        trigger={button}
        title={`${label} Kubernetes on ${profile}?`}
        description={description(profile)}
        confirmLabel={label}
        destructive
        onConfirm={() => onAction(action)}
      />
    )
  }
  return (
    <div role="group" aria-label="Kubernetes" className="flex items-center gap-1">
      <span className="text-xs text-muted-foreground">Kubernetes</span>
      {plain('kubernetes-start', 'Start')}
      {plain('kubernetes-stop', 'Stop')}
      {destructive('kubernetes-reset')}
      {destructive('kubernetes-delete')}
    </div>
  )
}
