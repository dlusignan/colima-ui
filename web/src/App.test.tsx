import { act, render, screen } from '@testing-library/react'
import { QueryClient } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import App from '@/App'
import { api } from '@/lib/api'
import { setTokenForTests } from '@/lib/auth/token'

function renderApp(path: string) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <MemoryRouter initialEntries={[path]}>
      <App client={client} />
    </MemoryRouter>,
  )
}

let fetchMock: ReturnType<typeof vi.fn>

beforeEach(() => {
  fetchMock = vi.fn()
  vi.stubGlobal('fetch', fetchMock)
})

afterEach(() => {
  vi.unstubAllGlobals()
  setTokenForTests(null)
})

describe('header', () => {
  it('shows the title, nav and theme switcher even without a token', () => {
    renderApp('/')
    expect(screen.getByRole('heading', { name: /colima ui/i })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Dashboard' })).toBeInTheDocument()
    expect(screen.getByRole('combobox', { name: /theme/i })).toBeInTheDocument()
  })
})

describe('auth gate', () => {
  it('missing token shows the alert and mounts no view', () => {
    renderApp('/')
    const alert = screen.getByRole('alert')
    expect(alert).toHaveTextContent(/reopen the link printed by the server/i)
    expect(alert).toHaveTextContent(/missing/i)
    expect(screen.queryByRole('heading', { name: 'Dashboard' })).not.toBeInTheDocument()
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('missing token hides profile routes too', () => {
    renderApp('/profiles/dev/config')
    expect(screen.getByRole('alert')).toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'Configuration' })).not.toBeInTheDocument()
  })

  it('a 401 swaps the view for the stale notice', async () => {
    setTokenForTests('tok')
    fetchMock.mockResolvedValue(
      new Response(JSON.stringify({ error: 'unauthorized' }), { status: 401 }),
    )
    renderApp('/')
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Dashboard' })).toBeInTheDocument()
    await act(async () => {
      await api.listProfiles().catch(() => undefined)
    })
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(/no longer valid|stale/i)
    expect(alert).toHaveTextContent(/reopen the link printed by the server/i)
    expect(screen.queryByRole('heading', { name: 'Dashboard' })).not.toBeInTheDocument()
    expect(screen.getByRole('combobox', { name: /theme/i })).toBeInTheDocument()
  })
})

describe('routes', () => {
  beforeEach(() => {
    setTokenForTests('tok')
    // The event stream opens once the token is ok; keep it pending.
    fetchMock.mockReturnValue(new Promise(() => {}))
  })

  it.each([
    ['/', 'Dashboard'],
    ['/profiles/dev', 'dev'],
  ])('renders %s', (path, title) => {
    renderApp(path)
    expect(screen.getByRole('heading', { name: title })).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(
      fetchMock.mock.calls.every(([url]) => url === '/api/events' || url === '/api/profiles'),
    ).toBe(true)
  })

  it('renders the capacity tab, loaded lazily', async () => {
    renderApp('/profiles/dev/capacity')
    expect(screen.getByRole('link', { name: 'Capacity' })).toHaveAttribute('aria-current', 'page')
    expect(await screen.findByText('Loading...')).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('renders the docker tab, loaded lazily', async () => {
    renderApp('/profiles/dev/docker')
    expect(screen.getByRole('link', { name: 'Docker' })).toHaveAttribute('aria-current', 'page')
    expect(await screen.findByText('Loading...')).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('renders the configuration tab and requests the config', () => {
    renderApp('/profiles/dev/config')
    expect(screen.getByRole('link', { name: 'Configuration' })).toHaveAttribute('aria-current', 'page')
    expect(screen.getByText('Loading configuration...')).toBeInTheDocument()
    expect(fetchMock.mock.calls.some(([url]) => url === '/api/profiles/dev/config')).toBe(true)
  })

  it('renders the operation log tab, loaded lazily', async () => {
    renderApp('/profiles/dev/log')
    expect(screen.getByRole('link', { name: 'Operation log' })).toHaveAttribute('aria-current', 'page')
    expect(await screen.findByText('Loading...')).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('profile routes show the profile name and tabs', () => {
    renderApp('/profiles/dev')
    expect(screen.getByRole('heading', { name: 'dev' })).toBeInTheDocument()
    const tabs = screen.getByRole('navigation', { name: 'Profile' })
    expect(tabs).toHaveTextContent('Detail')
    expect(tabs).toHaveTextContent('Operation log')
    expect(screen.getByRole('link', { name: 'Capacity' })).toHaveAttribute(
      'href',
      '/profiles/dev/capacity',
    )
    expect(screen.getByRole('link', { name: 'Docker' })).toHaveAttribute('href', '/profiles/dev/docker')
  })
})
