import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/lib/api/client'
import { api } from '@/lib/api/endpoints'
import type { Profile, ProfileContents } from '@/lib/api/types'
import { expectNoA11yViolations } from '@/test/axe'
import { profileFixture } from '@/test/fixtures'
import { createTestClient } from '@/test/queryWrapper'
import { DeleteProfileDialog } from './DeleteProfileDialog'

afterEach(() => vi.restoreAllMocks())

function setup(profile: Profile, props: { busy?: boolean; unavailable?: boolean } = {}) {
  const onDelete = vi.fn()
  const view = render(
    <QueryClientProvider client={createTestClient()}>
      <MemoryRouter>
        <DeleteProfileDialog profile={profile} busy={false} unavailable={false} onDelete={onDelete} {...props} />
      </MemoryRouter>
    </QueryClientProvider>,
  )
  return { onDelete, user: userEvent.setup(), ...view }
}

const dev = profileFixture({ name: 'dev', status: 'running' })
const empty: ProfileContents = { applicable: true, containers: 0, images: 0, volumes: 0 }
const open = (user: ReturnType<typeof userEvent.setup>) => user.click(screen.getByRole('button', { name: 'Delete dev' }))

describe('DeleteProfileDialog', () => {
  it('shows loading text while the contents are read', async () => {
    vi.spyOn(api, 'getContents').mockImplementation(() => new Promise(() => {}))
    const { user } = setup(dev)
    await open(user)
    const dialog = await screen.findByRole('dialog', { name: 'Delete profile dev?' })
    expect(within(dialog).getByText(/Checking what the profile holds/)).toBeInTheDocument()
    expect(within(dialog).queryByRole('textbox')).toBeNull()
  })

  it('a non-empty VM shows the counts and the Docker tab pointer, with no delete control', async () => {
    vi.spyOn(api, 'getContents').mockResolvedValue({ applicable: true, containers: 3, images: 1, volumes: 0 })
    const { user, baseElement } = setup(dev)
    await open(user)
    const dialog = await screen.findByRole('dialog', { name: 'Delete profile dev?' })
    expect(await within(dialog).findByText('Docker elements must be removed before this VM can be deleted.')).toBeInTheDocument()
    expect(dialog).toHaveTextContent('3 containers, 1 image and 0 volumes')
    expect(within(dialog).getByRole('link', { name: 'Docker tab' })).toHaveAttribute('href', '/profiles/dev/docker')
    expect(within(dialog).queryByRole('button', { name: 'Delete' })).toBeNull()
    expect(within(dialog).queryByRole('textbox')).toBeNull()
    await expectNoA11yViolations(baseElement)
    await user.click(within(dialog).getByRole('button', { name: 'Close' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  })

  it('an empty VM asks for the typed name; Cancel has focus, the box is unchecked, axe passes', async () => {
    vi.spyOn(api, 'getContents').mockResolvedValue(empty)
    const { user, onDelete, baseElement } = setup(dev)
    await open(user)
    const dialog = await screen.findByRole('alertdialog', { name: 'Delete profile dev?' })
    await waitFor(() => expect(within(dialog).getByRole('button', { name: 'Cancel' })).toHaveFocus())
    expect(within(dialog).queryByText(/cannot be checked/)).toBeNull()
    expect(within(dialog).getByRole('checkbox', { name: 'Also delete container runtime data' })).not.toBeChecked()
    const confirm = within(dialog).getByRole('button', { name: 'Delete' })
    expect(confirm).toBeDisabled()
    await user.type(within(dialog).getByRole('textbox'), 'Dev')
    expect(confirm).toBeDisabled()
    await expectNoA11yViolations(baseElement)
    await user.clear(within(dialog).getByRole('textbox'))
    await user.type(within(dialog).getByRole('textbox'), 'dev')
    await user.click(confirm)
    expect(onDelete).toHaveBeenCalledExactlyOnceWith(false)
  })

  it('sends deleteData true when the box is checked', async () => {
    vi.spyOn(api, 'getContents').mockResolvedValue(empty)
    const { user, onDelete } = setup(dev)
    await open(user)
    const dialog = await screen.findByRole('alertdialog')
    await user.click(within(dialog).getByRole('checkbox'))
    await user.type(within(dialog).getByRole('textbox'), 'dev')
    await user.click(within(dialog).getByRole('button', { name: 'Delete' }))
    expect(onDelete).toHaveBeenCalledExactlyOnceWith(true)
  })

  it('a double confirm in one batch calls onDelete once', async () => {
    vi.spyOn(api, 'getContents').mockResolvedValue(empty)
    const { user, onDelete } = setup(dev)
    await open(user)
    const dialog = await screen.findByRole('alertdialog')
    await user.type(within(dialog).getByRole('textbox'), 'dev')
    const confirm = within(dialog).getByRole('button', { name: 'Delete' })
    act(() => {
      confirm.click()
      confirm.click()
    })
    expect(onDelete).toHaveBeenCalledTimes(1)
  })

  it('a non-Docker runtime gets the confirmation with a cannot-check line', async () => {
    vi.spyOn(api, 'getContents').mockResolvedValue({ applicable: false })
    const { user } = setup(profileFixture({ name: 'dev', status: 'running', runtime: 'containerd' }))
    await open(user)
    const dialog = await screen.findByRole('alertdialog')
    expect(within(dialog).getByText(/Docker contents cannot be checked/)).toBeInTheDocument()
    expect(within(dialog).getByRole('textbox')).toBeInTheDocument()
  })

  it('a broken VM (status unknown) is not asked, shows the cannot-check line and keeps the typed name', async () => {
    const get = vi.spyOn(api, 'getContents')
    const { user, onDelete } = setup(profileFixture({ name: 'dev', status: 'unknown' }))
    await open(user)
    const dialog = await screen.findByRole('alertdialog')
    expect(within(dialog).getByText(/Docker contents cannot be checked/)).toBeInTheDocument()
    await user.type(within(dialog).getByRole('textbox'), 'dev')
    await user.click(within(dialog).getByRole('button', { name: 'Delete' }))
    expect(onDelete).toHaveBeenCalledWith(false)
    expect(get).not.toHaveBeenCalled()
  })

  it('a stopped VM says to start it first, with no delete control', async () => {
    vi.spyOn(api, 'getContents').mockRejectedValue(new ApiError(409, 'invalid_state', 'raw server text', { status: 'stopped' }))
    const { user } = setup(profileFixture({ name: 'dev', status: 'stopped' }))
    await open(user)
    const dialog = await screen.findByRole('dialog')
    expect(await within(dialog).findByText('Start the profile first so its Docker contents can be checked.')).toBeInTheDocument()
    expect(within(dialog).queryByRole('button', { name: 'Retry' })).toBeNull()
    expect(within(dialog).queryByRole('button', { name: 'Delete' })).toBeNull()
  })

  it('a contents error shows fixed text and Retry reads again', async () => {
    const get = vi
      .spyOn(api, 'getContents')
      .mockRejectedValueOnce(new ApiError(502, 'docker_unavailable', '/var/run/secret.sock down'))
      .mockResolvedValue(empty)
    const { user } = setup(dev)
    await open(user)
    const dialog = await screen.findByRole('dialog')
    expect(await within(dialog).findByText(/Docker did not answer/)).toBeInTheDocument()
    expect(dialog).not.toHaveTextContent('secret.sock')
    await user.click(within(dialog).getByRole('button', { name: 'Retry' }))
    expect(await screen.findByRole('alertdialog')).toBeInTheDocument()
    expect(get).toHaveBeenCalledTimes(2)
  })

  it('reads once per opening and again on reopening, never polls', async () => {
    const get = vi.spyOn(api, 'getContents').mockResolvedValue({ applicable: true, containers: 1, images: 0, volumes: 0 })
    const { user } = setup(dev)
    await open(user)
    const dialog = await screen.findByRole('dialog')
    await within(dialog).findByText(/must be removed/)
    await new Promise((r) => setTimeout(r, 100))
    expect(get).toHaveBeenCalledTimes(1)
    await user.click(within(dialog).getByRole('button', { name: 'Close' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    await open(user)
    await screen.findByRole('dialog')
    await waitFor(() => expect(get).toHaveBeenCalledTimes(2))
  })

  it('is disabled with a reason while busy or unavailable and never reads', async () => {
    const get = vi.spyOn(api, 'getContents')
    for (const props of [{ busy: true }, { unavailable: true }]) {
      const { user, unmount } = setup(dev, props)
      const button = screen.getByRole('button', { name: 'Delete dev' })
      expect(button.getAttribute('aria-disabled')).toBe('true')
      expect(button).toHaveAttribute('title')
      await user.click(button)
      expect(screen.queryByRole('dialog')).toBeNull()
      unmount()
    }
    expect(get).not.toHaveBeenCalled()
  })
})
