import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { profileFixture } from '@/test/fixtures'
import { DeleteProfileDialog } from './DeleteProfileDialog'

describe('DeleteProfileDialog', () => {
  it('calls onDelete once when Delete is confirmed twice in one batch', async () => {
    const onDelete = vi.fn()
    const user = userEvent.setup()
    render(
      <DeleteProfileDialog
        profile={profileFixture({ name: 'dev', status: 'stopped' })}
        busy={false}
        unavailable={false}
        onDelete={onDelete}
      />,
    )
    await user.click(screen.getByRole('button', { name: 'Delete profile dev' }))
    const dialog = await screen.findByRole('alertdialog')
    await waitFor(() => expect(within(dialog).getByRole('button', { name: 'Cancel' })).toHaveFocus())
    await user.type(within(dialog).getByRole('textbox'), 'dev')
    const confirm = within(dialog).getByRole('button', { name: 'Delete' })
    act(() => {
      confirm.click()
      confirm.click()
    })
    expect(onDelete).toHaveBeenCalledTimes(1)
    expect(onDelete).toHaveBeenCalledWith(false)
  })
})
