import { describe, expect, it } from 'vitest'
import { ApiError } from '@/lib/api/client'
import { describeKubernetesError, kubernetesVerb } from './useKubernetesAction'

describe('describeKubernetesError', () => {
  it('tells kubernetes_not_enabled apart from a stopped profile by details.reason only', () => {
    const off = new ApiError(409, 'invalid_state', 'ignored text', { status: 'running', reason: 'kubernetes_not_enabled' })
    expect(describeKubernetesError(off, 'kubernetes-reset', 'dev').message).toBe('Kubernetes is not enabled on dev.')
    const stopped = new ApiError(409, 'invalid_state', 'Kubernetes is not enabled', { status: 'stopped' })
    expect(describeKubernetesError(stopped, 'kubernetes-reset', 'dev').message).toBe(
      'dev is not running, so Kubernetes cannot be changed.',
    )
  })

  it('adopts the busy job and never shows server text', () => {
    const busy = new ApiError(409, 'profile_busy', 'secret server text', { jobId: 'j_1' })
    const d = describeKubernetesError(busy, 'kubernetes-stop', 'dev')
    expect(d).toMatchObject({ trackJobId: 'j_1', refresh: true })
    const other = new ApiError(500, 'internal_error', '/home/user/x')
    expect(describeKubernetesError(other, 'kubernetes-delete', 'dev').message).not.toContain('/home')
  })

  it('names the verb', () => {
    expect(kubernetesVerb('kubernetes-delete')).toBe('delete')
  })
})
