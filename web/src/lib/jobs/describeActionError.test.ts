import { describe, expect, it } from 'vitest'
import { ApiError } from '@/lib/api/client'
import { describeActionError } from './describeActionError'

const err = (status: number, code: string, details = {}, message = 'server text') =>
  new ApiError(status, code, message, details)

describe('describeActionError', () => {
  it('profile_busy adopts the running job and refreshes', () => {
    expect(describeActionError(err(409, 'profile_busy', { jobId: 'j_9' }), 'stop', 'dev')).toEqual({
      message: 'Another job is already running on dev.',
      refresh: true,
      trackJobId: 'j_9',
    })
  })

  it('profile_busy without a usable job id still refreshes', () => {
    const d = describeActionError(err(409, 'profile_busy', { jobId: 7 }), 'stop', 'dev')
    expect(d.trackJobId).toBeUndefined()
    expect(d.refresh).toBe(true)
  })

  it('invalid_state names the current status', () => {
    expect(
      describeActionError(err(409, 'invalid_state', { status: 'running' }), 'start', 'dev'),
    ).toEqual({ message: 'dev is already running.', refresh: true })
  })

  it('invalid_state without a status falls back', () => {
    const d = describeActionError(err(409, 'invalid_state'), 'start', 'dev')
    expect(d.message).toBe('dev is not in a state that allows start.')
    expect(d.refresh).toBe(true)
  })

  it('profile_not_found refreshes', () => {
    expect(describeActionError(err(404, 'profile_not_found'), 'start', 'dev')).toEqual({
      message: 'dev no longer exists.',
      refresh: true,
    })
  })

  it('colima_failed never shows the server text', () => {
    const d = describeActionError(err(502, 'colima_failed', {}, '/home/user/secret'), 'start', 'dev')
    expect(d.message).not.toContain('/home/user')
    expect(d.refresh).toBe(false)
  })

  it('shutting_down', () => {
    expect(describeActionError(err(503, 'shutting_down'), 'start', 'dev').message).toBe(
      'The server is shutting down.',
    )
  })

  it('network error (status 0)', () => {
    expect(describeActionError(err(0, 'network_error'), 'start', 'dev').message).toBe(
      'Could not reach the server.',
    )
  })

  it('401 gives no message', () => {
    expect(describeActionError(err(401, 'unauthorized'), 'start', 'dev').message).toBe('')
  })

  it('unknown codes use the ApiError message', () => {
    expect(describeActionError(err(500, 'boom', {}, 'Something broke'), 'start', 'dev').message).toBe(
      'Something broke',
    )
  })

  it('non ApiError values get a generic line', () => {
    expect(describeActionError(new Error('x'), 'restart', 'dev').message).toBe(
      'Could not restart dev.',
    )
  })
})
