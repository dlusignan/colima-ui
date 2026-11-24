import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/lib/api/client'
import { api } from '@/lib/api/endpoints'
import type { Profile } from '@/lib/api/types'
import { expectNoA11yViolations } from '@/test/axe'
import { jobFixture, profileFixture } from '@/test/fixtures'
import { createTestClient } from '@/test/queryWrapper'
import { Toaster } from '@/lib/toast/Toaster'
import { ToastProvider } from '@/lib/toast/ToastProvider'
import { CreateProfileProvider } from '@/views/create/CreateProfileProvider'
import { DashboardView } from './DashboardView'

afterEach(() => vi.restoreAllMocks())

function setup(profiles: Profile[] | Error) {
  const list = vi.spyOn(api, 'listProfiles')
  if (profiles instanceof Error) list.mockRejectedValue(profiles)
  else list.mockResolvedValue({ profiles })
  const view = render(
    <QueryClientProvider client={createTestClient()}>
      <MemoryRouter>
        <ToastProvider>
          <CreateProfileProvider>
            <DashboardView />
          </CreateProfileProvider>
          <Toaster />
        </ToastProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  )
  return { list, user: userEvent.setup(), ...view }
}

const running = profileFixture({ name: 'dev', status: 'running' })
const stopped = profileFixture({ name: 'prod', status: 'stopped', memoryBytes: 0 })

const isDisabled = (el: HTMLElement) => el.getAttribute('aria-disabled') === 'true'

describe('table', () => {
  it('lists every profile with its columns and passes axe', async () => {
    const { container } = setup([running, stopped])
    const table = await screen.findByRole('table', { name: 'Colima profiles' })
    expect(within(table).getAllByRole('columnheader').map((h) => h.textContent)).toEqual([
      'Name', 'Status', 'Runtime', 'CPUs', 'Memory', 'Disk', 'Colima', 'Source', 'Warnings', 'Actions',
    ])
    const row = within(table).getByRole('row', { name: /dev/ })
    expect(within(row).getByRole('rowheader', { name: 'dev' })).toBeInTheDocument()
    expect(within(row).getByText('running')).toBeInTheDocument()
    expect(within(row).getByText('docker')).toBeInTheDocument()
    expect(within(row).getByText('4 GiB')).toBeInTheDocument()
    expect(within(row).getByText('60 GiB')).toBeInTheDocument()
    expect(within(row).getByText('0.10.1')).toBeInTheDocument()
    expect(within(row).getByRole('link', { name: 'dev' })).toHaveAttribute('href', '/profiles/dev')
    const other = within(table).getByRole('row', { name: /prod/ })
    expect(within(other).getByText('stopped')).toBeInTheDocument()
    expect(within(other).getByText('—')).toBeInTheDocument()
    await expectNoA11yViolations(container)
  })

  it('enables buttons by status', async () => {
    setup([running, stopped, profileFixture({ name: 'odd', status: 'unknown' })])
    await screen.findByRole('table', { name: 'Colima profiles' })
    const b = (n: string) => screen.getByRole('button', { name: n })
    expect(isDisabled(b('Start dev'))).toBe(true)
    expect(isDisabled(b('Stop dev'))).toBe(false)
    expect(isDisabled(b('Restart dev'))).toBe(false)
    expect(isDisabled(b('Start prod'))).toBe(false)
    expect(isDisabled(b('Stop prod'))).toBe(true)
    expect(isDisabled(b('Restart prod'))).toBe(false)
    for (const a of ['Start', 'Stop', 'Restart', 'Delete']) expect(isDisabled(b(`${a} odd`))).toBe(false)
    for (const n of ['dev', 'prod']) expect(isDisabled(b(`Delete ${n}`))).toBe(false)
  })

  it('disables every button while the profile has a job and links the log', async () => {
    vi.spyOn(api, 'getJob').mockResolvedValue(jobFixture({ id: 'j_5', profile: 'dev', action: 'stop' }))
    setup([profileFixture({ name: 'dev', activeJobId: 'j_5' })])
    await screen.findByRole('table', { name: 'Colima profiles' })
    for (const a of ['Start', 'Stop', 'Restart']) {
      expect(isDisabled(screen.getByRole('button', { name: `${a} dev` }))).toBe(true)
    }
    expect(await screen.findByText('stopping')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Log dev' })).toHaveAttribute('href', '/profiles/dev/log?job=j_5')
  })

  it('tabs through name, start, stop, restart in order', async () => {
    const { user } = setup([stopped])
    await screen.findByRole('table', { name: 'Colima profiles' })
    await user.tab()
    expect(screen.getByRole('button', { name: 'New profile' })).toHaveFocus()
    await user.tab() // scroll region
    await user.tab()
    expect(screen.getByRole('link', { name: 'prod' })).toHaveFocus()
    await user.tab()
    expect(screen.getByRole('button', { name: 'Start prod' })).toHaveFocus()
    await user.tab()
    // Disabled Stop stays focusable.
    expect(screen.getByRole('button', { name: 'Stop prod' })).toHaveFocus()
    await user.tab()
    expect(screen.getByRole('button', { name: 'Restart prod' })).toHaveFocus()
  })
})

describe('actions', () => {
  it('start calls the route without confirmation and disables the row at once', async () => {
    const start = vi.spyOn(api, 'startProfile').mockResolvedValue({ jobId: 'j_2' })
    vi.spyOn(api, 'getJob').mockResolvedValue(jobFixture({ id: 'j_2', profile: 'prod', action: 'start' }))
    const { user } = setup([stopped])
    await user.click(await screen.findByRole('button', { name: 'Start prod' }))
    expect(start).toHaveBeenCalledWith('prod')
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    await waitFor(() => expect(isDisabled(screen.getByRole('button', { name: 'Restart prod' }))).toBe(true))
  })

  it('stop and restart confirm; cancel and Escape do not call the route', async () => {
    const stop = vi.spyOn(api, 'stopProfile').mockResolvedValue({ jobId: 'j_3' })
    const restart = vi.spyOn(api, 'restartProfile').mockResolvedValue({ jobId: 'j_4' })
    vi.spyOn(api, 'getJob').mockResolvedValue(jobFixture({ id: 'j_3', profile: 'dev', action: 'stop' }))
    const { user } = setup([running])
    await user.click(await screen.findByRole('button', { name: 'Stop dev' }))
    const dialog = await screen.findByRole('alertdialog', { name: 'Stop dev?' })
    await user.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
    await user.click(screen.getByRole('button', { name: 'Restart dev' }))
    await screen.findByRole('alertdialog', { name: 'Restart dev?' })
    await user.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
    expect(stop).not.toHaveBeenCalled()
    expect(restart).not.toHaveBeenCalled()

    await user.click(screen.getByRole('button', { name: 'Stop dev' }))
    const again = await screen.findByRole('alertdialog')
    await user.click(within(again).getByRole('button', { name: 'Stop' }))
    await waitFor(() => expect(stop).toHaveBeenCalledWith('dev'))
  })

  it('invalid_state shows an error toast, no inline box, and refreshes the list', async () => {
    vi.spyOn(api, 'startProfile').mockRejectedValue(
      new ApiError(409, 'invalid_state', 'x', { status: 'running' }),
    )
    const { user, list } = setup([stopped])
    await user.click(await screen.findByRole('button', { name: 'Start prod' }))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('prod is already running.')
    expect(screen.getAllByRole('alert')).toHaveLength(1)
    expect(screen.queryByRole('button', { name: /dismiss error/i })).not.toBeInTheDocument()
    expect(screen.getByRole('region', { name: 'Notifications' })).toContainElement(alert)
    await waitFor(() => expect(list).toHaveBeenCalledTimes(2))
  })

  it('profile_busy adopts the running job', async () => {
    vi.spyOn(api, 'startProfile').mockRejectedValue(
      new ApiError(409, 'profile_busy', 'x', { jobId: 'j_9' }),
    )
    vi.spyOn(api, 'getJob').mockResolvedValue(jobFixture({ id: 'j_9', profile: 'prod', action: 'restart' }))
    const { user } = setup([stopped])
    await user.click(await screen.findByRole('button', { name: 'Start prod' }))
    expect(await screen.findByText('restarting')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Log prod' })).toHaveAttribute('href', '/profiles/prod/log?job=j_9')
    expect(await screen.findByRole('alert')).toHaveTextContent('Another job is already running on prod.')
  })

  it('shows the progress label of a create job and adds no outcome text to the row', async () => {
    vi.spyOn(api, 'startProfile').mockRejectedValue(
      new ApiError(409, 'profile_busy', 'x', { jobId: 'j_c' }),
    )
    vi.spyOn(api, 'getJob')
      .mockResolvedValueOnce(jobFixture({ id: 'j_c', profile: 'prod', action: 'create' }))
      .mockResolvedValue(
        jobFixture({ id: 'j_c', profile: 'prod', action: 'create', state: 'failed', exitCode: 1 }),
      )
    const { user } = setup([stopped])
    await user.click(await screen.findByRole('button', { name: 'Start prod' }))
    expect(await screen.findByText('creating')).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByText('creating')).not.toBeInTheDocument(), { timeout: 4000 })
    expect(screen.queryByText(/create failed/)).not.toBeInTheDocument()
  }, 10_000)

  it('refetches the list when a job finishes and adds nothing under the status', async () => {
    vi.spyOn(api, 'startProfile').mockResolvedValue({ jobId: 'j_2' })
    const get = vi
      .spyOn(api, 'getJob')
      .mockResolvedValueOnce(jobFixture({ id: 'j_2', profile: 'prod', action: 'start' }))
      .mockResolvedValue(
        jobFixture({ id: 'j_2', profile: 'prod', action: 'start', state: 'failed', exitCode: 1 }),
      )
    const { user, list } = setup([stopped])
    await user.click(await screen.findByRole('button', { name: 'Start prod' }))
    await waitFor(() => expect(list.mock.calls.length).toBeGreaterThanOrEqual(3), { timeout: 4000 })
    expect(get).toHaveBeenCalled()
    expect(screen.queryByText(/start failed/)).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /outcome/i })).not.toBeInTheDocument()
    // The old hidden announcement is gone: the toast viewport is the only live region.
    expect(document.querySelectorAll('[aria-live]')).toHaveLength(1)
    expect(screen.getByRole('link', { name: 'Log prod' })).toBeInTheDocument()
  }, 10_000)
})

describe('states', () => {
  it('shows a loading skeleton', () => {
    vi.spyOn(api, 'listProfiles').mockReturnValue(new Promise(() => {}))
    render(
      <QueryClientProvider client={createTestClient()}>
        <MemoryRouter>
          <ToastProvider>
            <CreateProfileProvider>
              <DashboardView />
            </CreateProfileProvider>
          </ToastProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    )
    expect(screen.getByRole('table', { name: 'Loading profiles', busy: true })).toBeInTheDocument()
  })

  it('shows the empty state', async () => {
    setup([])
    expect(await screen.findByText(/No Colima profiles found/)).toBeInTheDocument()
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
  })

  it('shows an error with Retry that refetches', async () => {
    const { user, list } = setup(new ApiError(502, 'colima_failed', 'x'))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('Colima could not be queried.')
    list.mockResolvedValue({ profiles: [running] })
    await user.click(within(alert).getByRole('button', { name: 'Retry' }))
    expect(await screen.findByRole('table', { name: 'Colima profiles' })).toBeInTheDocument()
  })

  it('network error text', async () => {
    setup(new ApiError(0, 'network_error', 'x'))
    expect(await screen.findByRole('alert')).toHaveTextContent('Could not reach the server.')
  })

  it('shows one unavailable banner and disables the actions', async () => {
    const w = [{ code: 'colima_unavailable', message: 'down' }]
    setup([
      profileFixture({ name: 'a', status: 'unknown', warnings: w }),
      profileFixture({ name: 'b', status: 'unknown', warnings: w }),
    ])
    await screen.findByRole('table', { name: 'Colima profiles' })
    expect(screen.getAllByText(/Showing configuration only/)).toHaveLength(1)
    expect(screen.getAllByText('Colima unreachable')).toHaveLength(2)
    expect(isDisabled(screen.getByRole('button', { name: 'Restart a' }))).toBe(true)
  })

  it('shows one version banner for several profiles', async () => {
    const w = [{ code: 'colima_version_unsupported', message: 'Colima 0.9.1 is outside the tested line.' }]
    setup([
      profileFixture({ name: 'a', colimaVersion: '0.9.1', warnings: w }),
      profileFixture({ name: 'b', colimaVersion: '0.9.1', warnings: w }),
    ])
    await screen.findByRole('table', { name: 'Colima profiles' })
    expect(screen.getAllByText('Colima 0.9.1 is outside the tested line.', { selector: 'p' })).toHaveLength(1)
  })
})

describe('delete', () => {
  it('deletes from a row, stays on the dashboard and tracks the job', async () => {
    vi.spyOn(api, 'getContents').mockResolvedValue({ applicable: true, containers: 0, images: 0, volumes: 0 })
    const del = vi.spyOn(api, 'deleteProfile').mockResolvedValue({ jobId: 'j_9' })
    vi.spyOn(api, 'getJob').mockResolvedValue(jobFixture({ id: 'j_9', profile: 'dev', action: 'delete', state: 'running' }))
    const { user } = setup([running, stopped])
    await user.click(await screen.findByRole('button', { name: 'Delete dev' }))
    const dialog = await screen.findByRole('alertdialog', { name: 'Delete profile dev?' })
    await user.type(within(dialog).getByRole('textbox'), 'dev')
    await user.click(within(dialog).getByRole('button', { name: 'Delete' }))
    expect(del).toHaveBeenCalledExactlyOnceWith('dev', { confirm: 'dev', deleteData: false })
    expect(await screen.findByText('deleting')).toBeInTheDocument()
  })

  it('a stopped row says to start it first', async () => {
    vi.spyOn(api, 'getContents').mockRejectedValue(new ApiError(409, 'invalid_state', 'x', { status: 'stopped' }))
    const { user } = setup([stopped])
    await user.click(await screen.findByRole('button', { name: 'Delete prod' }))
    expect(await screen.findByText('Start the profile first so its Docker contents can be checked.')).toBeInTheDocument()
  })
})
