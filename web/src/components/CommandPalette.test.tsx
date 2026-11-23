import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { useState } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/lib/api/client'
import { useProfiles } from '@/lib/api/useProfiles'
import { api } from '@/lib/api/endpoints'
import type { Profile } from '@/lib/api/types'
import { shortcuts } from '@/lib/commands/shortcuts'
import { expectNoA11yViolations } from '@/test/axe'
import { profileFixture } from '@/test/fixtures'
import { createTestClient } from '@/test/queryWrapper'
import { CommandPalette } from './CommandPalette'
import { ShortcutsDialog } from './ShortcutsDialog'

afterEach(() => vi.restoreAllMocks())

function Where() {
  const l = useLocation()
  return <p data-testid="where">{l.pathname}</p>
}

function Shell() {
  // Rows come from the same query the palette reads, so a visible row means loaded data.
  const profiles = useProfiles().data?.profiles ?? []
  const [help, setHelp] = useState(false)
  return (
    <>
      <CommandPalette onOpenHelp={() => setHelp(true)} />
      <ShortcutsDialog open={help} onOpenChange={setHelp} />
      <input aria-label="Some field" />
      <textarea aria-label="Some area" />
      <table>
        <tbody>
          {profiles.map((p) => (
            <tr key={p.name} data-profile={p.name}>
              <td>
                <button>row {p.name}</button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <Where />
    </>
  )
}

function setup(profiles: Profile[], at = '/') {
  vi.spyOn(api, 'listProfiles').mockResolvedValue({ profiles })
  const start = vi.spyOn(api, 'startProfile').mockResolvedValue({ jobId: 'j1' })
  const stop = vi.spyOn(api, 'stopProfile').mockResolvedValue({ jobId: 'j2' })
  const restart = vi.spyOn(api, 'restartProfile').mockResolvedValue({ jobId: 'j3' })
  vi.spyOn(api, 'getJob').mockRejectedValue(new Error('no job'))
  const view = render(
    <QueryClientProvider client={createTestClient()}>
      <MemoryRouter initialEntries={[at]}>
        <Routes>
          <Route path="*" element={<Shell />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
  return { user: userEvent.setup(), start, stop, restart, ...view }
}

const a = profileFixture({ name: 'dev', status: 'running' })
const b = profileFixture({ name: 'prod', status: 'stopped' })
const palette = () => screen.findByRole('dialog', { name: 'Command palette' })

describe('palette', () => {
  it('Cmd+K opens it, Escape closes it, and it passes axe', async () => {
    const { user, container } = setup([a, b])
    await screen.findByRole('button', { name: 'row dev' })
    await user.keyboard('{Meta>}k{/Meta}')
    const dialog = await palette()
    expect(screen.getByRole('combobox', { name: 'Search commands' })).toHaveFocus()
    await expectNoA11yViolations(dialog)
    await user.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(container).toBeTruthy()
  })

  it('Ctrl+K toggles it and works inside an input', async () => {
    const { user } = setup([a])
    await user.click(screen.getByRole('textbox', { name: 'Some field' }))
    await user.keyboard('{Control>}k{/Control}')
    await palette()
    await user.keyboard('{Control>}k{/Control}')
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })

  it('filters by typing and navigates on Enter', async () => {
    const { user } = setup([a, b])
    await screen.findByRole('button', { name: 'row dev' })
    await user.keyboard('{Meta>}k{/Meta}')
    await palette()
    await user.keyboard('prod capacity')
    expect(screen.getAllByRole('option')).toHaveLength(1)
    await user.keyboard('{Enter}')
    expect(screen.getByTestId('where')).toHaveTextContent('/profiles/prod/capacity')
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })

  it('start runs at once without a confirmation', async () => {
    const { user, start } = setup([a, b])
    await screen.findByRole('button', { name: 'row dev' })
    await user.keyboard('{Meta>}k{/Meta}')
    await palette()
    await user.keyboard('Start prod{Enter}')
    await waitFor(() => expect(start).toHaveBeenCalledWith('prod'))
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(screen.getByRole('status')).toHaveTextContent('Starting prod.')
  })

  it('stop asks first and only the confirm button calls the endpoint', async () => {
    const { user, stop } = setup([a, b])
    await screen.findByRole('button', { name: 'row dev' })
    await user.keyboard('{Meta>}k{/Meta}')
    await palette()
    await user.keyboard('Stop dev{Enter}')
    const confirm = await screen.findByRole('alertdialog', { name: 'Stop dev?' })
    expect(stop).not.toHaveBeenCalled()
    await user.click(within(confirm).getByRole('button', { name: 'Cancel' }))
    expect(stop).not.toHaveBeenCalled()
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())

    await user.keyboard('{Meta>}k{/Meta}')
    await palette()
    await user.keyboard('Stop dev{Enter}')
    await user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Stop' }))
    await waitFor(() => expect(stop).toHaveBeenCalledWith('dev'))
  })

  it('shows a failure in an alert region', async () => {
    const { user, start } = setup([a, b])
    start.mockRejectedValue(new ApiError(409, 'profile_busy', 'busy', {}))
    await screen.findByRole('button', { name: 'row dev' })
    await user.keyboard('{Meta>}k{/Meta}')
    await palette()
    await user.keyboard('Start prod{Enter}')
    expect(await screen.findByRole('alert')).toHaveTextContent('Another job is already running on prod.')
  })

  it('a theme command changes data-theme', async () => {
    const { user } = setup([a])
    await user.keyboard('{Meta>}k{/Meta}')
    await palette()
    await user.keyboard('Theme: Nord{Enter}')
    expect(document.documentElement.dataset.theme).toBe('nord')
  })

  it('the help command opens the shortcuts dialog', async () => {
    const { user } = setup([a])
    await user.keyboard('{Meta>}k{/Meta}')
    await palette()
    await user.keyboard('Keyboard shortcuts{Enter}')
    expect(await screen.findByRole('dialog', { name: 'Keyboard shortcuts' })).toBeInTheDocument()
  })
})

describe('shortcuts', () => {
  it('act on the focused dashboard row', async () => {
    const { user, restart } = setup([a, b])
    await user.click(await screen.findByRole('button', { name: 'row prod' }))
    await user.keyboard('{Alt>}{Shift>}r{/Shift}{/Alt}')
    const confirm = await screen.findByRole('alertdialog', { name: 'Restart prod?' })
    await user.click(within(confirm).getByRole('button', { name: 'Restart' }))
    await waitFor(() => expect(restart).toHaveBeenCalledWith('prod'))
  })

  it('act on the route profile', async () => {
    const { user, start } = setup([a, b], '/profiles/prod/config')
    await screen.findByRole('button', { name: 'row dev' })
    await waitFor(() => expect(vi.mocked(api.listProfiles)).toHaveBeenCalled())
    await screen.findByRole('button', { name: 'row prod' })
    await user.keyboard('{Alt>}{Shift>}s{/Shift}{/Alt}')
    await waitFor(() => expect(start).toHaveBeenCalledWith('prod'))
  })

  it('act on the only profile', async () => {
    const { user, stop } = setup([a])
    await screen.findByRole('button', { name: 'row dev' })
    await user.keyboard('{Alt>}{Shift>}x{/Shift}{/Alt}')
    await user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Stop' }))
    await waitFor(() => expect(stop).toHaveBeenCalledWith('dev'))
  })

  it('ask for a profile when none is selected', async () => {
    const { user, start } = setup([a, b])
    await screen.findByRole('button', { name: 'row dev' })
    await user.keyboard('{Alt>}{Shift>}s{/Shift}{/Alt}')
    await palette()
    expect(screen.getByRole('combobox')).toHaveAttribute('placeholder', 'Pick a profile to start')
    expect(screen.getAllByRole('option').map((o) => o.textContent)).toEqual(['Start prod'])
    await user.keyboard('{Enter}')
    await waitFor(() => expect(start).toHaveBeenCalledWith('prod'))
  })

  it('are ignored while typing in an input or textarea', async () => {
    const { user, start } = setup([b])
    await screen.findByRole('button', { name: 'row prod' })
    await user.click(screen.getByRole('textbox', { name: 'Some field' }))
    await user.keyboard('{Alt>}{Shift>}s{/Shift}{/Alt}')
    await user.click(screen.getByRole('textbox', { name: 'Some area' }))
    await user.keyboard('{Alt>}{Shift>}s{/Shift}{/Alt}')
    await user.keyboard('{Meta>}[Slash]{/Meta}')
    expect(start).not.toHaveBeenCalled()
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('are ignored on key repeat', async () => {
    const { start } = setup([b])
    await screen.findByRole('button', { name: 'row prod' })
    const keys = { code: 'KeyS', altKey: true, shiftKey: true }
    fireEvent.keyDown(document.body, { ...keys, repeat: true })
    fireEvent.keyDown(document.body, { ...keys, repeat: true })
    expect(start).not.toHaveBeenCalled()
    fireEvent.keyDown(document.body, keys)
    await waitFor(() => expect(start).toHaveBeenCalledTimes(1))
  })

  it('are ignored while a dialog is open', async () => {
    const { user, start } = setup([b])
    await screen.findByRole('button', { name: 'row prod' })
    await user.keyboard('{Meta>}[Slash]{/Meta}')
    await screen.findByRole('dialog', { name: 'Keyboard shortcuts' })
    await user.keyboard('{Meta>}k{/Meta}')
    await user.keyboard('{Alt>}{Shift>}s{/Shift}{/Alt}')
    expect(screen.queryByRole('dialog', { name: 'Command palette' })).not.toBeInTheDocument()
    expect(start).not.toHaveBeenCalled()
  })

  it('Cmd+/ opens the help dialog, which lists every shortcut and passes axe', async () => {
    const { user } = setup([a])
    await user.keyboard('{Meta>}[Slash]{/Meta}')
    const dialog = await screen.findByRole('dialog', { name: 'Keyboard shortcuts' })
    for (const s of shortcuts) {
      expect(within(dialog).getByText(s.keys)).toBeInTheDocument()
      expect(within(dialog).getByText(s.description)).toBeInTheDocument()
    }
    await expectNoA11yViolations(dialog)
    await user.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })
})
