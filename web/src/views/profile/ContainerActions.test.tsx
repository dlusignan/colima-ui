import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/lib/api/client'
import { api } from '@/lib/api/endpoints'
import type { Container } from '@/lib/api/types'
import { Toaster } from '@/lib/toast/Toaster'
import { ToastProvider } from '@/lib/toast/ToastProvider'
import { expectNoA11yViolations } from '@/test/axe'
import { createTestClient } from '@/test/queryWrapper'
import { profileFixture } from '@/test/fixtures'
import { ContainersPanel } from './ContainersPanel'

afterEach(() => vi.restoreAllMocks())

const FULL = 'a'.repeat(64)
const make = (over: Partial<Container>): Container => ({
  id: 'aaaaaaaaaaaa',
  fullId: FULL,
  name: 'synthetic-web',
  image: 'synthetic/web:1',
  state: 'running',
  status: 'Up 2 hours',
  cpuPercent: 0,
  memoryBytes: 0,
  createdAt: '2026-01-01T00:00:00Z',
  ports: [],
  ...over,
})

function setup(containers: Container[], panel: { busy?: boolean; unavailable?: boolean } = {}) {
  const get = vi.spyOn(api, 'getContainers').mockResolvedValue({ containers })
  const act = vi.spyOn(api, 'containerAction')
  const view = render(
    <QueryClientProvider client={createTestClient()}>
      <MemoryRouter>
        <ToastProvider>
          <ContainersPanel profile={profileFixture({ name: 'dev', status: 'running', runtime: 'docker' })} {...panel} />
          <Toaster />
        </ToastProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  )
  return { get, act, user: userEvent.setup(), ...view }
}

const names = () => screen.queryAllByRole('button').map((b) => b.getAttribute('aria-label'))

describe('container actions', () => {
  it.each([
    ['Exited (0) 3 minutes ago', 'exited', ['Start synthetic-web']],
    ['Exited (137) 3 minutes ago', 'exited', ['Start synthetic-web']],
    ['Up 2 hours', 'running', ['Stop synthetic-web', 'Restart synthetic-web']],
    ['Up 2 hours (Paused)', 'paused', []],
    ['Created', 'created', []],
    ['Restarting (1) 5 seconds ago', 'restarting', []],
  ])('status %s offers the right buttons', async (status, state, expected) => {
    setup([make({ status, state })])
    await screen.findByRole('table')
    const found = names().filter((n) => n && !n.startsWith('Logs'))
    expect(found).toEqual(expected)
  })

  it('offers no actions for a row without a full id', async () => {
    setup([make({ fullId: '' })])
    await screen.findByRole('table')
    expect(names().filter((n) => n && !n.startsWith('Logs'))).toEqual([])
  })

  it('confirms with Cancel focused, sends nothing on cancel, and the full id once on confirm', async () => {
    const { act, get, user } = setup([make({})])
    let resolve: (v: { changed: boolean }) => void = () => {}
    act.mockReturnValue(new Promise((r) => (resolve = r)))
    await user.click(await screen.findByRole('button', { name: 'Stop synthetic-web' }))
    const dialog = await screen.findByRole('alertdialog')
    expect(within(dialog).getByText(/goes down briefly/)).toBeInTheDocument()
    expect(within(dialog).getByRole('button', { name: 'Cancel' })).toHaveFocus()
    await user.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    expect(act).not.toHaveBeenCalled()

    await user.click(screen.getByRole('button', { name: 'Stop synthetic-web' }))
    const confirm = within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Stop' })
    await user.dblClick(confirm)
    expect(act).toHaveBeenCalledTimes(1)
    expect(act).toHaveBeenCalledWith('dev', FULL, 'stop')
    const before = get.mock.calls.length
    resolve({ changed: true })
    expect(await screen.findByText('Stopped synthetic-web')).toBeInTheDocument()
    await waitFor(() => expect(get.mock.calls.length).toBeGreaterThan(before))
  })

  it('says already running when nothing changed', async () => {
    const { act, user } = setup([make({ status: 'Exited (0) 1 minute ago', state: 'exited' })])
    act.mockResolvedValue({ changed: false })
    await user.click(await screen.findByRole('button', { name: 'Start synthetic-web' }))
    await user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Start' }))
    expect(await screen.findByText('synthetic-web is already running')).toBeInTheDocument()
  })

  it('restart uses the restart route', async () => {
    const { act, user } = setup([make({})])
    act.mockResolvedValue({ changed: true })
    await user.click(await screen.findByRole('button', { name: 'Restart synthetic-web' }))
    await user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Restart' }))
    expect(act).toHaveBeenCalledWith('dev', FULL, 'restart')
    expect(await screen.findByText('Restarted synthetic-web')).toBeInTheDocument()
  })

  it.each([
    [new ApiError(404, 'not_found', 'SECRET server text'), /no longer exists/],
    [new ApiError(409, 'conflict', 'SECRET server text'), /refused to change/],
    [new ApiError(409, 'invalid_state', 'SECRET server text'), /VM is not running/],
    [new ApiError(409, 'profile_busy', 'SECRET server text'), /job is running/],
    [new ApiError(502, 'docker_unavailable', 'SECRET server text'), /Docker is not reachable/],
    [new ApiError(500, 'internal', 'SECRET server text'), /Could not stop synthetic-web/],
  ])('shows a fixed error text and refreshes the list (%#)', async (error, text) => {
    const { act, get, user } = setup([make({})])
    act.mockRejectedValue(error)
    await user.click(await screen.findByRole('button', { name: 'Stop synthetic-web' }))
    const before = get.mock.calls.length
    await user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Stop' }))
    const alert = (await screen.findAllByText(text))[0]
    expect(alert).toBeInTheDocument()
    expect(screen.queryByText(/SECRET/)).not.toBeInTheDocument()
    await waitFor(() => expect(get.mock.calls.length).toBeGreaterThan(before))
  })

  it.each([
    [{ busy: true }, 'A profile job is running.'],
    [{ unavailable: true }, 'Colima is unavailable.'],
  ])('disables the buttons with a reason (%j)', async (panel, reason) => {
    const { act, user } = setup([make({})], panel)
    const button = await screen.findByRole('button', { name: 'Stop synthetic-web' })
    expect(button).toHaveAttribute('aria-disabled', 'true')
    expect(screen.getAllByText(reason).length).toBeGreaterThan(0)
    await user.click(button)
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(act).not.toHaveBeenCalled()
  })

  it('passes axe with the dialog open', async () => {
    const { user } = setup([make({})])
    await user.click(await screen.findByRole('button', { name: 'Stop synthetic-web' }))
    await screen.findByRole('alertdialog')
    await expectNoA11yViolations(document.body)
  })
})
