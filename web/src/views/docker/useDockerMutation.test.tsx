import { act, renderHook, screen, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/lib/api/client'
import { queryKeys } from '@/lib/api/keys'
import { ToastProvider } from '@/lib/toast/ToastProvider'
import { Toaster } from '@/lib/toast/Toaster'
import { createTestClient, wrapperFor } from '@/test/queryWrapper'
import { describeDockerError } from './describeDockerError'
import { useDockerMutation } from './useDockerMutation'

beforeEach(() => vi.useFakeTimers({ shouldAdvanceTime: true }))
afterEach(() => vi.useRealTimers())

function setup(
  mutationFn: (v: string) => Promise<string>,
  extra: { onSuccess?: (d: string, v: string) => void; handleError?: (e: unknown, v: string) => boolean } = {},
) {
  const client = createTestClient()
  const invalidate = vi.spyOn(client, 'invalidateQueries')
  const Query = wrapperFor(client)
  const wrapper = ({ children }: { children: ReactNode }) => (
    <Query>
      <ToastProvider>
        {children}
        <Toaster />
      </ToastProvider>
    </Query>
  )
  const hook = renderHook(
    () =>
      useDockerMutation<string, string>({
        profile: 'synthetic-a',
        section: 'volumes',
        action: 'volume-remove',
        mutationFn,
        successTitle: (_d, v) => `Removed ${v}`,
        errorTitle: (v) => `Could not remove ${v}`,
        ...extra,
      }),
    { wrapper },
  )
  // Toaster lives in the same tree as the hook through the wrapper's children.
  return { ...hook, invalidate }
}

const run = async (fn: () => void) => {
  await act(async () => {
    fn()
    await vi.advanceTimersByTimeAsync(50)
  })
}

describe('useDockerMutation', () => {
  it('shows one success toast, invalidates the section and engine queries', async () => {
    const { result, invalidate } = setup(() => Promise.resolve('ok'))
    await run(() => result.current.run('vol-1'))
    expect(await screen.findAllByText('Removed vol-1')).toHaveLength(1)
    const keys = invalidate.mock.calls.map((c) => c[0]?.queryKey)
    expect(keys).toContainEqual(queryKeys.volumes('synthetic-a'))
    expect(keys).toContainEqual(queryKeys.engine('synthetic-a'))
  })

  it('is pending while the call runs', async () => {
    let release: (v: string) => void = () => {}
    const { result } = setup(() => new Promise<string>((r) => (release = r)))
    await run(() => result.current.run('vol-1'))
    expect(result.current.isPending).toBe(true)
    await run(() => release('ok'))
    await waitFor(() => expect(result.current.isPending).toBe(false))
  })

  it('shows a readable error toast for 409 and does not stack the same error', async () => {
    const { result, invalidate } = setup(() => Promise.reject(new ApiError(409, 'volume_in_use', 'raw server text')))
    await run(() => result.current.run('vol-1'))
    await run(() => result.current.run('vol-1'))
    expect(screen.queryByText('raw server text')).not.toBeInTheDocument()
    expect(screen.getAllByText('Could not remove vol-1')).toHaveLength(1)
    expect(screen.getAllByText(/in use or changed/)).toHaveLength(1)
    expect(invalidate).toHaveBeenCalled()
  })

  it('shows no toast for a rejected token', async () => {
    const { result } = setup(() => Promise.reject(new ApiError(401, 'unauthorized', 'nope')))
    await run(() => result.current.run('vol-1'))
    expect(screen.queryByText('Could not remove vol-1')).not.toBeInTheDocument()
  })

  it('falls back to the error title for a non-API failure', async () => {
    const { result } = setup(() => Promise.reject(new Error('boom')))
    await run(() => result.current.run('vol-1'))
    expect(screen.getAllByText('Could not remove vol-1').length).toBeGreaterThan(0)
    expect(screen.queryByText('boom')).not.toBeInTheDocument()
  })
})

describe('useDockerMutation callbacks', () => {
  it('calls onSuccess after the toast', async () => {
    const onSuccess = vi.fn()
    const { result } = setup(() => Promise.resolve('ok'), { onSuccess })
    await run(() => result.current.run('vol-1'))
    expect(onSuccess).toHaveBeenCalledWith('ok', 'vol-1')
    expect(screen.getAllByText('Removed vol-1')).toHaveLength(1)
  })

  it('shows no error toast when handleError takes the error, and still refetches', async () => {
    const handleError = vi.fn(() => true)
    const { result, invalidate } = setup(() => Promise.reject(new ApiError(409, 'conflict', 'x')), { handleError })
    await run(() => result.current.run('vol-1'))
    expect(handleError).toHaveBeenCalledTimes(1)
    expect(screen.queryByText('Could not remove vol-1')).not.toBeInTheDocument()
    expect(invalidate).toHaveBeenCalled()
  })

  it('still toasts when handleError returns false', async () => {
    const { result } = setup(() => Promise.reject(new ApiError(500, 'internal', 'x')), { handleError: () => false })
    await run(() => result.current.run('vol-1'))
    expect(screen.getAllByText('Could not remove vol-1').length).toBeGreaterThan(0)
  })
})

describe('describeDockerError', () => {
  it.each([
    [new ApiError(0, 'network', 'x'), 'Could not reach the server.'],
    [new ApiError(401, 'unauthorized', 'x'), ''],
    [new ApiError(503, 'docker_unavailable', 'x'), 'Docker is not reachable in this VM yet.'],
    [new ApiError(409, 'anything', 'x'), 'That is in use or changed since the list loaded. Refresh the list and try again.'],
    [new ApiError(422, 'invalid_name', 'x'), 'Docker rejected the request. Check the name or value and try again.'],
    [new ApiError(500, 'internal', 'Server said this'), 'Server said this'],
    [new Error('x'), 'fallback'],
  ])('maps %#', (error, text) => {
    expect(describeDockerError(error, 'fallback')).toBe(text)
  })
})

