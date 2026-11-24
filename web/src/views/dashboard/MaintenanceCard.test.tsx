import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/lib/api/client'
import { api } from '@/lib/api/endpoints'
import type { CachePruneAccepted } from '@/lib/api/types'
import { expectNoA11yViolations } from '@/test/axe'
import { jobFixture } from '@/test/fixtures'
import { createTestClient } from '@/test/queryWrapper'
import { Toaster } from '@/lib/toast/Toaster'
import { ToastProvider } from '@/lib/toast/ToastProvider'
import { MaintenanceCard } from './MaintenanceCard'

afterEach(() => vi.restoreAllMocks())

const accepted: CachePruneAccepted = { jobId: 'j_9', action: 'cache-prune', profile: '', state: 'running' }

function setup() {
  const view = render(
    <QueryClientProvider client={createTestClient()}>
      <MemoryRouter>
        <ToastProvider>
          <MaintenanceCard />
          <Toaster />
        </ToastProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  )
  return { user: userEvent.setup(), ...view }
}

const open = async (user: ReturnType<typeof userEvent.setup>) => {
  await user.click(screen.getByRole('button', { name: 'Clear download cache' }))
  return screen.findByRole('alertdialog')
}

describe('MaintenanceCard', () => {
  it('has a Maintenance card with the button and passes axe', async () => {
    const { container } = setup()
    expect(screen.getByRole('heading', { name: 'Maintenance' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Clear download cache' })).toBeEnabled()
    await expectNoA11yViolations(container)
  })

  it('the dialog explains the redownload, defaults to cancel, and cancelling sends nothing', async () => {
    const prune = vi.spyOn(api, 'pruneCache')
    const { user } = setup()
    const dialog = await open(user)
    expect(within(dialog).getByText(/may download its image again/)).toBeInTheDocument()
    expect(within(dialog).getByRole('checkbox', { name: 'Also clear Lima assets' })).not.toBeChecked()
    expect(within(dialog).getByRole('button', { name: 'Cancel' })).toHaveFocus()
    await expectNoA11yViolations(dialog)
    await user.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    expect(prune).not.toHaveBeenCalled()
  })

  it('sends the exact confirmation without --all by default, then follows the job', async () => {
    const prune = vi.spyOn(api, 'pruneCache').mockResolvedValue(accepted)
    const get = vi.spyOn(api, 'getJob').mockResolvedValue(
      jobFixture({ id: 'j_9', profile: '', action: 'cache-prune', output: [{ seq: 1, line: 'removing' }] }),
    )
    const { user } = setup()
    await user.click(within(await open(user)).getByRole('button', { name: 'Clear cache' }))
    expect(prune).toHaveBeenCalledExactlyOnceWith({ confirm: 'delete-cached-assets', includeLima: false })
    expect(await screen.findByText('Clearing the download cache…')).toBeInTheDocument()
    expect(await screen.findByText('removing')).toBeInTheDocument()
    expect(get).toHaveBeenCalledWith('j_9', expect.anything())
    // While it runs the button is gone: no second request is possible.
    expect(screen.queryByRole('button', { name: 'Clear download cache' })).not.toBeInTheDocument()
  })

  it('sends includeLima when the box is checked, and the box is unchecked on reopen', async () => {
    const prune = vi.spyOn(api, 'pruneCache').mockRejectedValue(new ApiError(409, 'conflict', 'x', {}))
    const { user } = setup()
    let dialog = await open(user)
    await user.click(within(dialog).getByRole('checkbox', { name: 'Also clear Lima assets' }))
    await user.click(within(dialog).getByRole('button', { name: 'Clear cache' }))
    expect(prune).toHaveBeenCalledExactlyOnceWith({ confirm: 'delete-cached-assets', includeLima: true })
    await screen.findByRole('alert')
    dialog = await open(user)
    expect(within(dialog).getByRole('checkbox', { name: 'Also clear Lima assets' })).not.toBeChecked()
  })

  it('a double confirm sends one request', async () => {
    let resolve: (a: CachePruneAccepted) => void = () => {}
    const prune = vi.spyOn(api, 'pruneCache').mockReturnValue(new Promise((r) => (resolve = r)))
    vi.spyOn(api, 'getJob').mockResolvedValue(jobFixture({ id: 'j_9', profile: '' }))
    const { user } = setup()
    const dialog = await open(user)
    const confirm = within(dialog).getByRole('button', { name: 'Clear cache' })
    await user.dblClick(confirm)
    expect(prune).toHaveBeenCalledTimes(1)
    await act(async () => resolve(accepted))
    expect(prune).toHaveBeenCalledTimes(1)
  })

  it.each([
    [new ApiError(409, 'invalid_state', 'server text /home/u', { reason: 'assets_in_use', status: 'starting' }), /created or started/],
    [new ApiError(409, 'conflict', 'server text', {}), /already being cleared/],
    [new ApiError(422, 'confirmation_required', 'server text /home/u', {}), /Could not clear/],
    [new ApiError(0, 'network_error', 'x', {}), /Could not reach the server/],
  ])('shows fixed text for a refusal and allows another try', async (error, text) => {
    vi.spyOn(api, 'pruneCache').mockRejectedValue(error)
    const { user } = setup()
    await user.click(within(await open(user)).getByRole('button', { name: 'Clear cache' }))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(text)
    expect(alert).not.toHaveTextContent('/home')
    expect(screen.getByRole('button', { name: 'Clear download cache' })).toBeEnabled()
  })

  it('cancel asks the server once and the card returns when the job ends', async () => {
    vi.spyOn(api, 'pruneCache').mockResolvedValue(accepted)
    const get = vi.spyOn(api, 'getJob').mockResolvedValue(jobFixture({ id: 'j_9', profile: '' }))
    let finish: () => void = () => {}
    const cancel = vi.spyOn(api, 'cancelJob').mockImplementation(
      () => new Promise((r) => (finish = () => r(jobFixture({ id: 'j_9', state: 'cancelled' })))),
    )
    const { user } = setup()
    await user.click(within(await open(user)).getByRole('button', { name: 'Clear cache' }))
    const button = await screen.findByRole('button', { name: 'Cancel' })
    await user.click(button)
    await user.click(screen.getByRole('button', { name: 'Cancelling…' }))
    expect(cancel).toHaveBeenCalledExactlyOnceWith('j_9')
    expect(screen.getAllByText('Cancelling…').length).toBeGreaterThan(0)
    get.mockResolvedValue(jobFixture({ id: 'j_9', profile: '', state: 'cancelled' }))
    await act(async () => finish())
    await waitFor(() => expect(screen.getByRole('button', { name: 'Clear download cache' })).toBeEnabled())
  })

  it('a job that is gone after a backend restart frees the card', async () => {
    vi.spyOn(api, 'pruneCache').mockResolvedValue(accepted)
    vi.spyOn(api, 'getJob').mockRejectedValue(new ApiError(404, 'job_not_found', 'x', {}))
    const { user } = setup()
    await user.click(within(await open(user)).getByRole('button', { name: 'Clear cache' }))
    await waitFor(() => expect(screen.getByRole('button', { name: 'Clear download cache' })).toBeEnabled())
  })
})
