import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from './ConfirmDialog'

function setup() {
  const onConfirm = vi.fn()
  render(
    <ConfirmDialog
      trigger={<Button>Stop synthetic-a</Button>}
      title="Stop synthetic-a?"
      description="Its containers stop until you start it again."
      confirmLabel="Stop"
      destructive
      onConfirm={onConfirm}
    />,
  )
  return { onConfirm, user: userEvent.setup() }
}

describe('ConfirmDialog', () => {
  it('opens as an alert dialog with Cancel focused', async () => {
    const { user } = setup()
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Stop synthetic-a' }))
    const dialog = await screen.findByRole('alertdialog', { name: 'Stop synthetic-a?' })
    expect(dialog).toHaveAccessibleDescription('Its containers stop until you start it again.')
    await waitFor(() => expect(screen.getByRole('button', { name: 'Cancel' })).toHaveFocus())
  })

  it('does not confirm on Cancel and returns focus to the trigger', async () => {
    const { user, onConfirm } = setup()
    const trigger = screen.getByRole('button', { name: 'Stop synthetic-a' })
    await user.click(trigger)
    await user.click(await screen.findByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
    expect(onConfirm).not.toHaveBeenCalled()
    await waitFor(() => expect(trigger).toHaveFocus())
  })

  it('cancels on Escape', async () => {
    const { user, onConfirm } = setup()
    await user.click(screen.getByRole('button', { name: 'Stop synthetic-a' }))
    await screen.findByRole('alertdialog')
    await user.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
    expect(onConfirm).not.toHaveBeenCalled()
  })

  it('confirms once and closes', async () => {
    const { user, onConfirm } = setup()
    await user.click(screen.getByRole('button', { name: 'Stop synthetic-a' }))
    await user.click(await screen.findByRole('button', { name: 'Stop' }))
    expect(onConfirm).toHaveBeenCalledTimes(1)
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
  })

  it('works controlled and without a trigger', async () => {
    const onConfirm = vi.fn()
    const onOpenChange = vi.fn()
    const user = userEvent.setup()
    render(
      <ConfirmDialog
        open
        onOpenChange={onOpenChange}
        title="Stop synthetic-a?"
        description="d"
        confirmLabel="Stop"
        onConfirm={onConfirm}
      />,
    )
    await user.click(await screen.findByRole('button', { name: 'Stop' }))
    expect(onConfirm).toHaveBeenCalledTimes(1)
    expect(onOpenChange).toHaveBeenCalledWith(false)
  })
})
