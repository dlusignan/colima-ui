import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

async function load() {
  vi.resetModules()
  return import('./token')
}

describe('token capture', () => {
  beforeEach(() => {
    window.history.replaceState(null, '', '/profiles/dev?x=1')
  })
  afterEach(() => {
    window.history.replaceState(null, '', '/')
  })

  it('reads the token from the fragment and strips it, keeping path and query', async () => {
    window.history.replaceState(null, '', '/profiles/dev?x=1#token=abc123')
    const spy = vi.spyOn(window.history, 'replaceState')
    const t = await load()
    t.captureTokenFromLocation()
    expect(t.getToken()).toBe('abc123')
    expect(t.getAuthStatus()).toBe('ok')
    expect(window.location.hash).toBe('')
    expect(window.location.pathname + window.location.search).toBe('/profiles/dev?x=1')
    expect(spy).toHaveBeenCalledTimes(1)
  })

  it('is idempotent', async () => {
    window.history.replaceState(null, '', '/#token=abc123')
    const t = await load()
    t.captureTokenFromLocation()
    const spy = vi.spyOn(window.history, 'replaceState')
    t.captureTokenFromLocation()
    expect(spy).not.toHaveBeenCalled()
    expect(t.getToken()).toBe('abc123')
  })

  it('status is missing when there is no fragment', async () => {
    const t = await load()
    t.captureTokenFromLocation()
    expect(t.getToken()).toBeNull()
    expect(t.getAuthStatus()).toBe('missing')
  })

  it('never writes to localStorage or sessionStorage', async () => {
    window.history.replaceState(null, '', '/#token=abc123')
    const set = vi.spyOn(Storage.prototype, 'setItem')
    const t = await load()
    t.captureTokenFromLocation()
    t.markTokenRejected()
    expect(set).not.toHaveBeenCalled()
    expect(JSON.stringify(localStorage)).not.toContain('abc123')
    expect(JSON.stringify(sessionStorage)).not.toContain('abc123')
  })

  it('markTokenRejected flips status and notifies subscribers once', async () => {
    window.history.replaceState(null, '', '/#token=abc123')
    const t = await load()
    t.captureTokenFromLocation()
    const listener = vi.fn()
    t.subscribeAuth(listener)
    t.markTokenRejected()
    t.markTokenRejected()
    expect(t.getAuthStatus()).toBe('rejected')
    expect(listener).toHaveBeenCalledTimes(1)
  })
})
