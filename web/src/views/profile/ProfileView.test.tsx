import { act, render, screen, waitFor, within } from '@testing-library/react'
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
import { Toaster } from '@/lib/toast/Toaster'
import { ToastProvider } from '@/lib/toast/ToastProvider'
import { ProfileView } from './ProfileView'

afterEach(() => vi.restoreAllMocks())

const container = { id: 'c1', fullId: '', name: 'synthetic-web', image: 'synthetic/web:1', state: 'running', cpuPercent: 1.25, memoryBytes: 64 * 1024 ** 2, status: 'Up 2 hours', createdAt: '2026-01-01T00:00:00Z', ports: [] }

function setup(profiles: Profile[] | Error, at = '/profiles/dev') {
  const list = vi.spyOn(api, 'listProfiles')
  if (profiles instanceof Error) list.mockRejectedValue(profiles)
  else list.mockResolvedValue({ profiles })
  const containers = vi.spyOn(api, 'getContainers').mockResolvedValue({ containers: [container] })
  const actions = vi.spyOn(api, 'getActions').mockResolvedValue({
    entries: [{ startedAt: '2026-01-01T10:00:00Z', action: 'start', outcome: 'success', detail: null }],
  })
  const kubernetes = vi.spyOn(api, 'getKubernetes').mockResolvedValue({ enabled: false })
  const view = render(
    <QueryClientProvider client={createTestClient()}>
      <MemoryRouter initialEntries={[at]}>
        <ToastProvider>
          <Routes>
            <Route path="/profiles/:name" element={<ProfileView />} />
            <Route path="/" element={<p>dashboard page</p>} />
          </Routes>
          <Toaster />
        </ToastProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  )
  return { list, containers, actions, kubernetes, user: userEvent.setup(), ...view }
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

  it('shows an error toast and no inline error box', async () => {
    vi.spyOn(api, 'startProfile').mockRejectedValue(new ApiError(409, 'invalid_state', 'x', { status: 'running' }))
    const { user } = setup([stopped])
    await user.click(await screen.findByRole('button', { name: 'Start dev' }))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('dev is already running.')
    expect(screen.getAllByRole('alert')).toHaveLength(1)
    expect(screen.getByRole('region', { name: 'Notifications' })).toContainElement(alert)
    expect(screen.queryByRole('button', { name: /dismiss error/i })).not.toBeInTheDocument()
  })

  it('profile_busy shows the busy message as an error toast', async () => {
    vi.spyOn(api, 'startProfile').mockRejectedValue(new ApiError(409, 'profile_busy', 'x', { jobId: 'j_9' }))
    vi.spyOn(api, 'getJob').mockResolvedValue(jobFixture({ id: 'j_9', profile: 'dev', action: 'restart' }))
    const { user } = setup([stopped])
    await user.click(await screen.findByRole('button', { name: 'Start dev' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Another job is already running on dev.')
    expect(screen.queryByRole('button', { name: /dismiss error/i })).not.toBeInTheDocument()
  })

  it('adds no outcome line under the status when a job fails', async () => {
    vi.spyOn(api, 'startProfile').mockResolvedValue({ jobId: 'j_2' })
    vi.spyOn(api, 'getJob').mockResolvedValue(
      jobFixture({ id: 'j_2', profile: 'dev', action: 'start', state: 'failed', exitCode: 1 }),
    )
    const { user } = setup([stopped])
    await user.click(await screen.findByRole('button', { name: 'Start dev' }))
    expect(await screen.findByRole('link', { name: 'Open log' }, { timeout: 4000 })).toBeInTheDocument()
    expect(screen.queryByText(/start failed/)).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /outcome/i })).not.toBeInTheDocument()
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
    { ...base, id: 'p', name: 'synthetic-paused', state: 'paused', status: 'Up 5 minutes (Paused)' },
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
    const paused = within(table).getByRole('row', { name: /synthetic-paused/ })
    expect(within(paused).getByText('(paused)')).toBeInTheDocument()
    expect(within(up).queryByText('(paused)')).not.toBeInTheDocument()
    const crash = within(table).getByRole('row', { name: /synthetic-crash/ })
    expect(within(crash).getByText('(crashed)')).toBeInTheDocument()
    const created = within(up).getByText(/ago$|just now/, { selector: 'span[title]' })
    expect(created).toHaveAttribute('title', new Date('2026-01-01T00:00:00Z').toLocaleString())
    await expectNoA11yViolations(root)
  })
})

describe('delete', () => {
  it('is disabled with a reason while the profile runs', async () => {
    setup([running])
    const button = await screen.findByRole('button', { name: 'Delete profile dev' })
    expect(isDisabled(button)).toBe(true)
    expect(screen.getByText('Stop the profile first.')).toBeInTheDocument()
  })

  it('needs the typed name, sends deleteData false by default and goes back to the dashboard on success', async () => {
    const del = vi.spyOn(api, 'deleteProfile').mockResolvedValue({ jobId: 'j_9' })
    const getJob = vi.spyOn(api, 'getJob').mockResolvedValue(
      jobFixture({ id: 'j_9', profile: 'dev', action: 'delete', state: 'running' }),
    )
    const { user, list } = setup([stopped])
    await user.click(await screen.findByRole('button', { name: 'Delete profile dev' }))
    const dialog = await screen.findByRole('alertdialog', { name: 'Delete profile dev?' })
    await waitFor(() => expect(within(dialog).getByRole('button', { name: 'Cancel' })).toHaveFocus())
    const confirm = within(dialog).getByRole('button', { name: 'Delete' })
    expect(isDisabled(confirm) || (confirm as HTMLButtonElement).disabled).toBe(true)
    expect(within(dialog).getByRole('checkbox', { name: 'Also delete container runtime data' })).not.toBeChecked()
    await user.type(within(dialog).getByRole('textbox'), 'dev')
    list.mockResolvedValue({ profiles: [] })
    getJob.mockResolvedValue(jobFixture({ id: 'j_9', profile: 'dev', action: 'delete', state: 'succeeded', exitCode: 0 }))
    await user.click(confirm)
    expect(del).toHaveBeenCalledWith('dev', { confirm: 'dev', deleteData: false })
    expect(await screen.findByText('dashboard page')).toBeInTheDocument()
  })

  it('sends deleteData true when the box is checked', async () => {
    const del = vi.spyOn(api, 'deleteProfile').mockResolvedValue({ jobId: 'j_9' })
    vi.spyOn(api, 'getJob').mockResolvedValue(jobFixture({ id: 'j_9', profile: 'dev', action: 'delete' }))
    const { user } = setup([stopped])
    await user.click(await screen.findByRole('button', { name: 'Delete profile dev' }))
    const dialog = await screen.findByRole('alertdialog')
    await user.click(within(dialog).getByRole('checkbox', { name: 'Also delete container runtime data' }))
    await user.type(within(dialog).getByRole('textbox'), 'dev')
    await user.click(within(dialog).getByRole('button', { name: 'Delete' }))
    expect(del).toHaveBeenCalledWith('dev', { confirm: 'dev', deleteData: true })
    expect(await screen.findByText('deleting')).toBeInTheDocument()
  })

  it('does nothing on a wrong name or on Cancel, and resets the box', async () => {
    const del = vi.spyOn(api, 'deleteProfile').mockResolvedValue({ jobId: 'j_9' })
    const { user } = setup([stopped])
    await user.click(await screen.findByRole('button', { name: 'Delete profile dev' }))
    let dialog = await screen.findByRole('alertdialog')
    await user.click(within(dialog).getByRole('checkbox'))
    await user.type(within(dialog).getByRole('textbox'), 'Dev')
    await user.click(within(dialog).getByRole('button', { name: 'Delete' }))
    expect(del).not.toHaveBeenCalled()
    await user.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
    await user.click(screen.getByRole('button', { name: 'Delete profile dev' }))
    dialog = await screen.findByRole('alertdialog')
    expect(within(dialog).getByRole('checkbox')).not.toBeChecked()
    expect(within(dialog).getByRole('textbox')).toHaveValue('')
  })

  it('sends one request when Delete is clicked twice', async () => {
    const del = vi.spyOn(api, 'deleteProfile').mockImplementation(() => new Promise(() => {}))
    const { user } = setup([stopped])
    await user.click(await screen.findByRole('button', { name: 'Delete profile dev' }))
    const dialog = await screen.findByRole('alertdialog')
    await user.type(within(dialog).getByRole('textbox'), 'dev')
    const confirm = within(dialog).getByRole('button', { name: 'Delete' })
    // Both clicks land in one batch, before React can close and unmount the dialog.
    act(() => {
      confirm.click()
      confirm.click()
    })
    await waitFor(() => expect(del).toHaveBeenCalledTimes(1))
    await new Promise((r) => setTimeout(r, 50))
    expect(del).toHaveBeenCalledTimes(1)
  })

  it('is enabled for an unknown status (a broken VM) and the server decides', async () => {
    setup([profileFixture({ name: 'dev', status: 'unknown' })])
    const button = await screen.findByRole('button', { name: 'Delete profile dev' })
    expect(isDisabled(button)).toBe(false)
  })

  it('shows a toast when the server says the profile is running', async () => {
    vi.spyOn(api, 'deleteProfile').mockRejectedValue(new ApiError(409, 'invalid_state', 'x', { status: 'running' }))
    const { user } = setup([stopped])
    await user.click(await screen.findByRole('button', { name: 'Delete profile dev' }))
    const dialog = await screen.findByRole('alertdialog')
    await user.type(within(dialog).getByRole('textbox'), 'dev')
    await user.click(within(dialog).getByRole('button', { name: 'Delete' }))
    expect(await screen.findByText('Stop dev before deleting it.')).toBeInTheDocument()
  })
})

describe('kubernetes group', () => {
  it('is hidden while kubernetes is off', async () => {
    const { kubernetes } = setup([running])
    await screen.findByRole('heading', { name: 'Configuration' })
    await waitFor(() => expect(kubernetes).toHaveBeenCalledWith('dev', expect.anything()))
    expect(screen.queryByRole('group', { name: 'Kubernetes' })).not.toBeInTheDocument()
  })

  it('is not even asked for while the profile is stopped', async () => {
    const { kubernetes } = setup([stopped])
    await screen.findByRole('heading', { name: 'Configuration' })
    expect(kubernetes).not.toHaveBeenCalled()
    expect(screen.queryByRole('group', { name: 'Kubernetes' })).not.toBeInTheDocument()
  })

  it('is hidden when the probe fails', async () => {
    const { kubernetes } = setup([running])
    kubernetes.mockRejectedValue(new ApiError(502, 'colima_failed', 'x'))
    await screen.findByRole('heading', { name: 'Configuration' })
    expect(screen.queryByRole('group', { name: 'Kubernetes' })).not.toBeInTheDocument()
  })

  it('shows four buttons when kubernetes is on and start sends no confirmation', async () => {
    const start = vi.spyOn(api, 'kubernetesStart').mockResolvedValue({ jobId: 'j_7' })
    vi.spyOn(api, 'getJob').mockResolvedValue(jobFixture({ id: 'j_7', profile: 'dev', action: 'kubernetes-start' }))
    const { kubernetes, user } = setup([running])
    kubernetes.mockResolvedValue({ enabled: true })
    const group = await screen.findByRole('group', { name: 'Kubernetes' })
    expect(within(group).getAllByRole('button').map((b) => b.textContent)).toEqual(['Start', 'Stop', 'Reset', 'Delete'])
    await user.click(within(group).getByRole('button', { name: 'Start Kubernetes on dev' }))
    expect(start).toHaveBeenCalledWith('dev')
    expect(await screen.findByText('starting Kubernetes')).toBeInTheDocument()
    await waitFor(() => expect(isDisabled(screen.getByRole('button', { name: 'Reset Kubernetes on dev' }))).toBe(true))
  })

  it('reset and delete explain the destruction and call the route only after confirming', async () => {
    const reset = vi.spyOn(api, 'kubernetesReset').mockResolvedValue({ jobId: 'j_8' })
    const del = vi.spyOn(api, 'kubernetesDelete').mockResolvedValue({ jobId: 'j_9' })
    vi.spyOn(api, 'getJob').mockResolvedValue(jobFixture({ id: 'j_8', profile: 'dev', action: 'kubernetes-reset' }))
    const { kubernetes, user } = setup([running])
    kubernetes.mockResolvedValue({ enabled: true })
    await user.click(await screen.findByRole('button', { name: 'Delete Kubernetes on dev' }))
    const dialog = await screen.findByRole('alertdialog')
    expect(dialog).toHaveTextContent('workloads and cluster state are destroyed')
    await user.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    expect(del).not.toHaveBeenCalled()

    await user.click(screen.getByRole('button', { name: 'Reset Kubernetes on dev' }))
    const second = await screen.findByRole('alertdialog')
    expect(second).toHaveTextContent('workloads and cluster state are destroyed')
    expect(reset).not.toHaveBeenCalled()
    await user.click(within(second).getByRole('button', { name: 'Reset' }))
    await waitFor(() => expect(reset).toHaveBeenCalledTimes(1))
    expect(reset).toHaveBeenCalledWith('dev')
  })

  it('ignores a second submit while the first is in flight', async () => {
    let resolve: (v: { jobId: string }) => void = () => {}
    const stop = vi.spyOn(api, 'kubernetesStop').mockReturnValue(new Promise((r) => (resolve = r)))
    vi.spyOn(api, 'getJob').mockResolvedValue(jobFixture({ id: 'j_5', profile: 'dev', action: 'kubernetes-stop' }))
    const { kubernetes, user } = setup([running])
    kubernetes.mockResolvedValue({ enabled: true })
    const button = await screen.findByRole('button', { name: 'Stop Kubernetes on dev' })
    await user.click(button)
    await waitFor(() => expect(isDisabled(button)).toBe(true))
    await user.click(button)
    expect(stop).toHaveBeenCalledTimes(1)
    resolve({ jobId: 'j_5' })
  })

  it('shows the kubernetes_not_enabled conflict as a toast', async () => {
    vi.spyOn(api, 'kubernetesStart').mockRejectedValue(
      new ApiError(409, 'invalid_state', 'x', { status: 'running', reason: 'kubernetes_not_enabled' }),
    )
    const { kubernetes, user } = setup([running])
    kubernetes.mockResolvedValue({ enabled: true })
    await user.click(await screen.findByRole('button', { name: 'Start Kubernetes on dev' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Kubernetes is not enabled on dev.')
  })

  it('reads the flag again when a kubernetes job finishes', async () => {
    vi.spyOn(api, 'kubernetesStart').mockResolvedValue({ jobId: 'j_6' })
    vi.spyOn(api, 'getJob').mockResolvedValue(
      jobFixture({ id: 'j_6', profile: 'dev', action: 'kubernetes-start', state: 'succeeded', exitCode: 0 }),
    )
    const { kubernetes, user } = setup([running])
    kubernetes.mockResolvedValue({ enabled: true })
    await user.click(await screen.findByRole('button', { name: 'Start Kubernetes on dev' }))
    await waitFor(() => expect(kubernetes.mock.calls.length).toBeGreaterThanOrEqual(2), { timeout: 4000 })
  }, 10_000)
})
