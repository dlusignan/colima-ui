import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/lib/api/client'
import { api } from '@/lib/api/endpoints'
import type { Profile } from '@/lib/api/types'
import { expectNoA11yViolations } from '@/test/axe'
import { jobFixture, profileFixture } from '@/test/fixtures'
import { createTestClient } from '@/test/queryWrapper'
import { ProfileView } from './ProfileView'

afterEach(() => vi.restoreAllMocks())

const container = { id: 'c1', name: 'synthetic-web', image: 'synthetic/web:1', state: 'running', cpuPercent: 1.25, memoryBytes: 64 * 1024 ** 2, status: 'Up 2 hours', createdAt: '2026-01-01T00:00:00Z', ports: [] }

function setup(profiles: Profile[] | Error, at = '/profiles/dev') {
  const list = vi.spyOn(api, 'listProfiles')
  if (profiles instanceof Error) list.mockRejectedValue(profiles)
  else list.mockResolvedValue({ profiles })
  const containers = vi.spyOn(api, 'getContainers').mockResolvedValue({ containers: [container] })
  const actions = vi.spyOn(api, 'getActions').mockResolvedValue({
    entries: [{ startedAt: '2026-01-01T10:00:00Z', action: 'start', outcome: 'success', detail: null }],
  })
  const view = render(
    <QueryClientProvider client={createTestClient()}>
      <MemoryRouter initialEntries={[at]}>
        <Routes>
          <Route path="/profiles/:name" element={<ProfileView />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
  return { list, containers, actions, user: userEvent.setup(), ...view }
}

const running = profileFixture({ name: 'dev', status: 'running' })
const stopped = profileFixture({ name: 'dev', status: 'stopped' })
const isDisabled = (el: HTMLElement) => el.getAttribute('aria-disabled') === 'true'

describe('page', () => {
  it('shows summary, containers and actions for the profile in the URL, and passes axe', async () => {
    const { container: root, containers, actions } = setup([profileFixture({ name: 'other' }), running])
    expect(await screen.findByRole('heading', { name: 'Configuration' })).toBeInTheDocument()
    const dl = screen.getByText('Runtime').closest('dl') as HTMLElement
    expect(within(dl).getByText('docker')).toBeInTheDocument()
    expect(within(dl).getByText('4 GiB')).toBeInTheDocument()
    expect(within(dl).getByText('60 GiB')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Edit configuration' })).toHaveAttribute('href', '/profiles/dev/config')
    expect(screen.getByRole('link', { name: 'Capacity' })).toHaveAttribute('href', '/profiles/dev/capacity')
    const table = await screen.findByRole('table', { name: 'Containers in dev' })
    expect(within(table).getByRole('rowheader', { name: 'synthetic-web' })).toBeInTheDocument()
    expect(within(table).getByText('1.3%')).toBeInTheDocument()
    expect(containers).toHaveBeenCalledWith('dev', expect.anything())
    expect(await screen.findByText('start')).toBeInTheDocument()
    expect(actions).toHaveBeenCalledWith('dev', 20, expect.anything())
    await expectNoA11yViolations(root)
  })

  it('shows 404 content for an unknown name', async () => {
    const { containers } = setup([running], '/profiles/nope')
    expect(await screen.findByText('No profile named nope.')).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: '404' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Back to the dashboard' })).toHaveAttribute('href', '/')
    expect(containers).not.toHaveBeenCalled()
  })

  it('shows an error with Retry when the list fails', async () => {
    const { user, list } = setup(new ApiError(502, 'colima_failed', 'x'))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('Colima could not be queried.')
    list.mockResolvedValue({ profiles: [running] })
    await user.click(within(alert).getByRole('button', { name: 'Retry' }))
    expect(await screen.findByRole('heading', { name: 'Configuration' })).toBeInTheDocument()
  })

  it('shows the warning banner', async () => {
    setup([profileFixture({ name: 'dev', status: 'unknown', warnings: [{ code: 'colima_unavailable', message: 'down' }] })])
    expect(await screen.findByText(/Showing configuration only/)).toBeInTheDocument()
    expect(isDisabled(screen.getByRole('button', { name: 'Restart dev' }))).toBe(true)
  })
})

describe('containers', () => {
  it('does not request containers while the VM is stopped', async () => {
    const { containers } = setup([stopped])
    expect(await screen.findByText('Start the profile to see its containers.')).toBeInTheDocument()
    expect(containers).not.toHaveBeenCalled()
  })

  it('explains docker_unavailable', async () => {
    const { containers } = setup([running])
    containers.mockRejectedValue(new ApiError(502, 'docker_unavailable', 'x'))
    expect(await screen.findByText('Docker is not reachable in this VM yet.')).toBeInTheDocument()
  })

  it('shows the empty state per runtime', async () => {
    const { containers, unmount } = setup([running])
    containers.mockResolvedValue({ containers: [] })
    expect(await screen.findByText('No containers.')).toBeInTheDocument()
    unmount()
    const second = setup([profileFixture({ name: 'dev', runtime: 'containerd' })])
    second.containers.mockResolvedValue({ containers: [] })
    expect(await screen.findByText('Container data is available for the docker runtime only.')).toBeInTheDocument()
  })
})

describe('actions list', () => {
  it('shows an inline state on any error', async () => {
    const { actions } = setup([running])
    actions.mockRejectedValue(new ApiError(404, 'not_found', 'x'))
    expect(await screen.findByText('Action history is not available.')).toBeInTheDocument()
  })

  it('shows the empty state', async () => {
    const { actions } = setup([running])
    actions.mockResolvedValue({ entries: [] })
    expect(await screen.findByText('No actions recorded yet.')).toBeInTheDocument()
  })
})

describe('controls', () => {
  it('start calls the route and disables the buttons during the job, with a log link', async () => {
    const start = vi.spyOn(api, 'startProfile').mockResolvedValue({ jobId: 'j_2' })
    vi.spyOn(api, 'getJob').mockResolvedValue(jobFixture({ id: 'j_2', profile: 'dev', action: 'start' }))
    const { user } = setup([stopped])
    await user.click(await screen.findByRole('button', { name: 'Start dev' }))
    expect(start).toHaveBeenCalledWith('dev')
    await waitFor(() => expect(isDisabled(screen.getByRole('button', { name: 'Restart dev' }))).toBe(true))
    expect(await screen.findByText('starting')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Open log' })).toHaveAttribute('href', '/profiles/dev/log?job=j_2')
  })

  it('stop confirms before calling the route', async () => {
    const stop = vi.spyOn(api, 'stopProfile').mockResolvedValue({ jobId: 'j_3' })
    vi.spyOn(api, 'getJob').mockResolvedValue(jobFixture({ id: 'j_3', profile: 'dev', action: 'stop' }))
    const { user } = setup([running])
    await user.click(await screen.findByRole('button', { name: 'Stop dev' }))
    const dialog = await screen.findByRole('alertdialog', { name: 'Stop dev?' })
    expect(stop).not.toHaveBeenCalled()
    await user.click(within(dialog).getByRole('button', { name: 'Stop' }))
    await waitFor(() => expect(stop).toHaveBeenCalledWith('dev'))
  })

  it('restart confirms before calling the route', async () => {
    const restart = vi.spyOn(api, 'restartProfile').mockResolvedValue({ jobId: 'j_4' })
    vi.spyOn(api, 'getJob').mockResolvedValue(jobFixture({ id: 'j_4', profile: 'dev', action: 'restart' }))
    const { user } = setup([running])
    await user.click(await screen.findByRole('button', { name: 'Restart dev' }))
    const dialog = await screen.findByRole('alertdialog', { name: 'Restart dev?' })
    await user.click(within(dialog).getByRole('button', { name: 'Restart' }))
    await waitFor(() => expect(restart).toHaveBeenCalledWith('dev'))
  })

  it('shows an error that can be dismissed', async () => {
    vi.spyOn(api, 'startProfile').mockRejectedValue(new ApiError(409, 'invalid_state', 'x', { status: 'running' }))
    const { user } = setup([stopped])
    await user.click(await screen.findByRole('button', { name: 'Start dev' }))
    const alert = await screen.findByText('dev is already running.')
    expect(alert).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: /dismiss error/i }))
    expect(screen.queryByText('dev is already running.')).not.toBeInTheDocument()
  })

  it('holds a failed outcome with a dismiss button', async () => {
    vi.spyOn(api, 'startProfile').mockResolvedValue({ jobId: 'j_2' })
    vi.spyOn(api, 'getJob').mockResolvedValue(
      jobFixture({ id: 'j_2', profile: 'dev', action: 'start', state: 'failed', exitCode: 1 }),
    )
    const { user } = setup([stopped])
    await user.click(await screen.findByRole('button', { name: 'Start dev' }))
    expect(await screen.findByText(/start failed \(exit 1\)/, {}, { timeout: 4000 })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Open log' })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Dismiss dev outcome' }))
    expect(screen.queryByText(/start failed \(exit/)).not.toBeInTheDocument()
  }, 10_000)
})

describe('containers table details', () => {
  const base = { ...container, createdAt: '2026-01-01T00:00:00Z' }
  const fixtures = [
    { ...base, id: 'a', name: 'synthetic-up', status: 'Up 2 hours', ports: [
      { containerPort: 80, protocol: 'tcp', hostIp: '0.0.0.0', hostPort: 8080 },
      { containerPort: 80, protocol: 'tcp', hostIp: '::', hostPort: 8080 },
      { containerPort: 5432, protocol: 'tcp', hostIp: null, hostPort: null },
    ] },
    { ...base, id: 'b', name: 'synthetic-done', state: 'exited', status: 'Exited (0) 3 days ago' },
    { ...base, id: 'c', name: 'synthetic-crash', state: 'exited', status: 'Exited (137) 2 hours ago' },
  ]

  it('shows status text, ports and created time, and marks a crash in text', async () => {
    const { containers, container: root } = setup([running])
    containers.mockResolvedValue({ containers: fixtures })
    const table = await screen.findByRole('table', { name: 'Containers in dev' })
    for (const h of ['Status', 'Ports', 'Created']) {
      expect(within(table).getByRole('columnheader', { name: h })).toHaveAttribute('scope', 'col')
    }
    const up = within(table).getByRole('row', { name: /synthetic-up/ })
    expect(within(up).getByText('Up 2 hours')).toBeInTheDocument()
    expect(within(up).getByText('8080:80/tcp')).toBeInTheDocument()
    expect(within(up).getAllByText('8080:80/tcp')).toHaveLength(1)
    expect(within(up).getByText('5432/tcp')).toBeInTheDocument()
    const done = within(table).getByRole('row', { name: /synthetic-done/ })
    expect(within(done).getByText('Exited (0) 3 days ago')).toBeInTheDocument()
    expect(within(done).getByText('None')).toBeInTheDocument()
    expect(within(done).queryByText('(crashed)')).not.toBeInTheDocument()
    const crash = within(table).getByRole('row', { name: /synthetic-crash/ })
    expect(within(crash).getByText('(crashed)')).toBeInTheDocument()
    const created = within(up).getByText(/ago$|just now/, { selector: 'span[title]' })
    expect(created).toHaveAttribute('title', new Date('2026-01-01T00:00:00Z').toLocaleString())
    await expectNoA11yViolations(root)
  })
})
