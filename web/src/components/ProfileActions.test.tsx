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
})
