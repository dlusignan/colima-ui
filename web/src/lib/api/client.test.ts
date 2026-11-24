import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { getAuthStatus, setTokenForTests } from '@/lib/auth/token'
import { ApiError, request } from './client'
import { api } from './endpoints'

function jsonResponse(status: number, body: unknown) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

let fetchMock: ReturnType<typeof vi.fn>

beforeEach(() => {
  fetchMock = vi.fn()
  vi.stubGlobal('fetch', fetchMock)
  setTokenForTests('tok')
})

afterEach(() => {
  vi.unstubAllGlobals()
  setTokenForTests(null)
})

async function failure(p: Promise<unknown>): Promise<ApiError> {
  try {
    await p
  } catch (e) {
    expect(e).toBeInstanceOf(ApiError)
    return e as ApiError
  }
  throw new Error('expected rejection')
}

describe('request', () => {
  it('sends the bearer token and a relative URL on GET without a body header', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { profiles: [] }))
    await expect(api.listProfiles()).resolves.toEqual({ profiles: [] })
    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe('/api/profiles')
    expect(init.method).toBe('GET')
    expect(init.headers.Authorization).toBe('Bearer tok')
    expect(init.headers['Content-Type']).toBeUndefined()
    expect(init.body).toBeUndefined()
  })

  it('sends a JSON body with Content-Type on PUT', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, {}))
    await api.updateConfig('dev', { cpu: 6 })
    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe('/api/profiles/dev/config')
    expect(init.method).toBe('PUT')
    expect(init.headers.Authorization).toBe('Bearer tok')
    expect(init.headers['Content-Type']).toBe('application/json')
    expect(init.body).toBe('{"cpu":6}')
  })

  it('sends the creation request as a JSON POST', async () => {
    fetchMock.mockResolvedValue(jsonResponse(202, { jobId: 'j_1', profile: 'dev' }))
    const out = await api.createProfile({ name: 'dev', cpus: 4, kubernetes: false })
    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe('/api/profiles')
    expect(init.method).toBe('POST')
    expect(init.headers['Content-Type']).toBe('application/json')
    expect(init.body).toBe('{"name":"dev","cpus":4,"kubernetes":false}')
    expect(out).toEqual({ jobId: 'j_1', profile: 'dev' })
  })

  it('sends POST without body or Content-Type and escapes names', async () => {
    fetchMock.mockResolvedValue(jsonResponse(202, { jobId: 'j_1' }))
    await api.startProfile('a/b c')
    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe('/api/profiles/a%2Fb%20c/start')
    expect(init.headers.Authorization).toBe('Bearer tok')
    expect(init.headers['Content-Type']).toBeUndefined()
  })

  it('builds the volume write requests and treats a 204 as success', async () => {
    fetchMock.mockResolvedValue(jsonResponse(201, { name: 'data' }))
    await api.createVolume('dev', { name: 'data', labels: { k: 'v' } })
    let [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe('/api/profiles/dev/volumes')
    expect(init.method).toBe('POST')
    expect(init.body).toBe('{"name":"data","labels":{"k":"v"}}')

    fetchMock.mockResolvedValue(jsonResponse(200, { name: 'a/b' }))
    await api.getVolume('dev', 'a/b')
    ;[url, init] = fetchMock.mock.calls[1]
    expect(url).toBe('/api/profiles/dev/volumes/a%2Fb')
    expect(init.method).toBe('GET')

    fetchMock.mockResolvedValue(new Response(null, { status: 204 }))
    await expect(api.removeVolume('dev', 'data')).resolves.toBeUndefined()
    ;[url, init] = fetchMock.mock.calls[2]
    expect(url).toBe('/api/profiles/dev/volumes/data')
    expect(init.method).toBe('DELETE')
    expect(init.body).toBeUndefined()
    expect(url).not.toContain('force')

    fetchMock.mockResolvedValue(
      jsonResponse(200, {
        removed: [],
        removedCount: 0,
        truncated: false,
        reclaimedBytes: 0,
        includesNamedVolumes: true,
        resultComplete: true,
      }),
    )
    await api.pruneVolumes('dev')
    ;[url, init] = fetchMock.mock.calls[3]
    expect(url).toBe('/api/profiles/dev/volumes/prune')
    expect(init.method).toBe('POST')
    expect(init.body).toBe('{"confirm":"delete-unused-volumes"}')
  })

  it('builds the network write requests and treats a 204 as success', async () => {
    fetchMock.mockResolvedValue(jsonResponse(201, { id: 'a', name: 'backend' }))
    await api.createNetwork('dev', { name: 'backend', internal: true, subnet: '172.28.0.0/16' })
    let [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe('/api/profiles/dev/networks')
    expect(init.method).toBe('POST')
    expect(init.body).toBe('{"name":"backend","internal":true,"subnet":"172.28.0.0/16"}')

    fetchMock.mockResolvedValue(jsonResponse(200, { name: 'a/b' }))
    await api.getNetwork('dev', 'a/b')
    ;[url, init] = fetchMock.mock.calls[1]
    expect(url).toBe('/api/profiles/dev/networks/a%2Fb')
    expect(init.method).toBe('GET')

    fetchMock.mockResolvedValue(new Response(null, { status: 204 }))
    await expect(api.removeNetwork('dev', 'backend')).resolves.toBeUndefined()
    ;[url, init] = fetchMock.mock.calls[2]
    expect(url).toBe('/api/profiles/dev/networks/backend')
    expect(init.method).toBe('DELETE')
    expect(init.body).toBeUndefined()
    expect(url).not.toContain('force')

    fetchMock.mockResolvedValue(
      jsonResponse(200, { removed: [], removedCount: 0, truncated: false, resultComplete: true }),
    )
    await api.pruneNetworks('dev')
    ;[url, init] = fetchMock.mock.calls[3]
    expect(url).toBe('/api/profiles/dev/networks/prune')
    expect(init.method).toBe('POST')
    expect(init.body).toBe('{"confirm":"delete-unused-networks"}')
  })

  it('decodes the in-use conflict of a network removal', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(409, {
        error: { code: 'conflict', message: 'm', details: { name: 'backend', containers: 2 } },
      }),
    )
    const e = await failure(api.removeNetwork('dev', 'backend'))
    expect(e.status).toBe(409)
    expect(e.details.containers).toBe(2)
  })

  it('accepts an empty 204 only where the caller expects one', async () => {
    fetchMock.mockImplementation(() => Promise.resolve(new Response(null, { status: 204 })))
    const e = await failure(api.getVolume('dev', 'data'))
    expect(e.code).toBe('bad_response')
    const e2 = await failure(request('POST', '/api/x'))
    expect(e2.code).toBe('bad_response')
    await expect(request('DELETE', '/api/x', { expectEmpty: true })).resolves.toBeUndefined()
    // An error status is still an error with expectEmpty.
    fetchMock.mockResolvedValue(
      jsonResponse(409, { error: { code: 'conflict', message: 'm', details: {} } }),
    )
    expect((await failure(request('DELETE', '/api/x', { expectEmpty: true }))).status).toBe(409)
  })

  it('decodes the in-use conflict of a volume removal', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(409, {
        error: { code: 'conflict', message: 'm', details: { name: 'data', containers: 2 } },
      }),
    )
    const e = await failure(api.removeVolume('dev', 'data'))
    expect(e.status).toBe(409)
    expect(e.details.containers).toBe(2)
  })

  it('builds query strings for metrics and actions', async () => {
    fetchMock.mockImplementation(() => Promise.resolve(jsonResponse(200, {})))
    await api.getMetrics('dev', { scope: 'vm', maxPoints: 50 })
    await api.getActions('dev', 5)
    await api.getActions('dev')
    expect(fetchMock.mock.calls[0][0]).toBe('/api/profiles/dev/metrics?scope=vm&maxPoints=50')
    expect(fetchMock.mock.calls[1][0]).toBe('/api/profiles/dev/actions?limit=5')
    expect(fetchMock.mock.calls[2][0]).toBe('/api/profiles/dev/actions')
  })

  it('builds the container logs path and tail query', async () => {
    fetchMock.mockImplementation(() => Promise.resolve(jsonResponse(200, { lines: [], truncated: false })))
    await api.getContainerLogs('a b', 'aaaaaaaaaaaa', 50)
    await api.getContainerLogs('dev', 'aaaaaaaaaaaa')
    expect(fetchMock.mock.calls[0][0]).toBe('/api/profiles/a%20b/containers/aaaaaaaaaaaa/logs?tail=50')
    expect(fetchMock.mock.calls[1][0]).toBe('/api/profiles/dev/containers/aaaaaaaaaaaa/logs')
  })

  it('decodes the contract error shape (profile_busy)', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(409, {
        error: { code: 'profile_busy', message: 'busy', details: { jobId: 'j_8f' } },
      }),
    )
    const e = await failure(api.startProfile('dev'))
    expect(e).toMatchObject({
      status: 409,
      code: 'profile_busy',
      message: 'busy',
      details: { jobId: 'j_8f' },
    })
  })

  it('decodes validation_failed fields', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(422, {
        error: {
          code: 'validation_failed',
          message: 'no',
          details: { fields: { cpu: 'must be between 1 and 16' } },
        },
      }),
    )
    const e = await failure(api.updateConfig('dev', { cpu: 99 }))
    expect(e.status).toBe(422)
    expect(e.details).toEqual({ fields: { cpu: 'must be between 1 and 16' } })
  })

  it('decodes the plain string error shape', async () => {
    fetchMock.mockResolvedValue(jsonResponse(403, { error: 'forbidden_origin' }))
    const e = await failure(request('GET', '/api/x'))
    expect(e).toMatchObject({
      status: 403,
      code: 'forbidden_origin',
      message: 'forbidden_origin',
      details: {},
    })
  })

  it('maps a non-JSON error body to bad_response', async () => {
    fetchMock.mockResolvedValue(new Response('<html>bad gateway</html>', { status: 502 }))
    const e = await failure(request('GET', '/api/x'))
    expect(e).toMatchObject({ status: 502, code: 'bad_response' })
  })

  it('maps an unrecognised JSON error body to bad_response', async () => {
    fetchMock.mockResolvedValue(jsonResponse(500, { oops: true }))
    expect((await failure(request('GET', '/api/x'))).code).toBe('bad_response')
  })

  it('maps a non-JSON success body to bad_response', async () => {
    fetchMock.mockResolvedValue(new Response('nope', { status: 200 }))
    expect((await failure(request('GET', '/api/x'))).code).toBe('bad_response')
  })

  it('maps a rejected fetch to network_error with status 0', async () => {
    fetchMock.mockRejectedValue(new TypeError('failed'))
    const e = await failure(request('GET', '/api/x'))
    expect(e).toMatchObject({ status: 0, code: 'network_error' })
  })

  it('sends no request without a token', async () => {
    setTokenForTests(null)
    const e = await failure(request('GET', '/api/x'))
    expect(e.code).toBe('missing_token')
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('a 401 flips the auth status to rejected', async () => {
    fetchMock.mockResolvedValue(jsonResponse(401, { error: 'unauthorized' }))
    expect(getAuthStatus()).toBe('ok')
    const e = await failure(request('GET', '/api/x'))
    expect(e.status).toBe(401)
    expect(getAuthStatus()).toBe('rejected')
  })

  it('a 403 does not touch the auth status', async () => {
    fetchMock.mockResolvedValue(jsonResponse(403, { error: 'forbidden_host' }))
    await failure(request('GET', '/api/x'))
    expect(getAuthStatus()).toBe('ok')
  })
})
