import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { profileFixture } from '@/test/fixtures'
import { ProfileActions } from './ProfileActions'

const off = (n: string) => screen.getByRole('button', { name: n }).getAttribute('aria-disabled') === 'true'

describe('ProfileActions', () => {
  it('start needs no confirmation', async () => {
    const onAction = vi.fn()
    render(
      <ProfileActions
        profile={profileFixture({ name: 'dev', status: 'stopped' })}
        busy={false}
        unavailable={false}
        onAction={onAction}
      />,
    )
    await userEvent.click(screen.getByRole('button', { name: 'Start dev' }))
    expect(onAction).toHaveBeenCalledWith('start')
  })

  it('stop confirms first', async () => {
    const onAction = vi.fn()
    render(
      <ProfileActions profile={profileFixture({ name: 'dev' })} busy={false} unavailable={false} onAction={onAction} />,
    )
    await userEvent.click(screen.getByRole('button', { name: 'Stop dev' }))
    expect(onAction).not.toHaveBeenCalled()
    await userEvent.click(await screen.findByRole('button', { name: 'Stop' }))
    expect(onAction).toHaveBeenCalledWith('stop')
  })

  it('disables everything when busy or unavailable', () => {
    for (const props of [{ busy: true, unavailable: false }, { busy: false, unavailable: true }]) {
      const { unmount } = render(
        <ProfileActions profile={profileFixture({ name: 'dev', status: 'unknown' })} {...props} onAction={vi.fn()} />,
      )
      expect([off('Start dev'), off('Stop dev'), off('Restart dev')]).toEqual([true, true, true])
      unmount()
    }
  })

  it('update runtime shows only when asked, is enabled only when running and confirms first', async () => {
    const onAction = vi.fn()
    const { rerender } = render(
      <ProfileActions profile={profileFixture({ name: 'dev' })} busy={false} unavailable={false} onAction={onAction} />,
    )
    expect(screen.queryByRole('button', { name: 'Update runtime dev' })).toBeNull()

    rerender(
      <ProfileActions profile={profileFixture({ name: 'dev', status: 'stopped' })} busy={false} unavailable={false} onAction={onAction} showUpdate />,
    )
    expect(off('Update runtime dev')).toBe(true)

    rerender(
      <ProfileActions profile={profileFixture({ name: 'dev', status: 'running' })} busy unavailable={false} onAction={onAction} showUpdate />,
    )
    expect(off('Update runtime dev')).toBe(true)

    rerender(
      <ProfileActions profile={profileFixture({ name: 'dev', status: 'running' })} busy={false} unavailable={false} onAction={onAction} showUpdate />,
    )
    await userEvent.click(screen.getByRole('button', { name: 'Update runtime dev' }))
    expect(onAction).not.toHaveBeenCalled()
    const dialog = await screen.findByRole('alertdialog')
    expect(dialog).toHaveTextContent('containers may restart briefly')
    await userEvent.click(screen.getByRole('button', { name: 'Update runtime' }))
    expect(onAction).toHaveBeenCalledWith('update')
  })
})
