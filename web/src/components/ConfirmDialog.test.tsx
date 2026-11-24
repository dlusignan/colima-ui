import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from './ConfirmDialog'
import { expectNoA11yViolations } from '@/test/axe'

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

describe('ConfirmDialog extras', () => {
  function setupExtras() {
    const onConfirm = vi.fn()
    render(
      <ConfirmDialog
        trigger={<Button>Remove synthetic-data</Button>}
        title="Remove synthetic-data?"
        description="The data is deleted."
        confirmLabel="Remove"
        destructive
        items={['synthetic-data', 'synthetic-cache']}
        requireText="synthetic-data"
        onConfirm={onConfirm}
      />,
    )
    return { onConfirm, user: userEvent.setup() }
  }

  it('lists the affected items', async () => {
    const { user } = setupExtras()
    await user.click(screen.getByRole('button', { name: 'Remove synthetic-data' }))
    const list = await screen.findByRole('list', { name: 'Affected items' })
    expect(list.querySelectorAll('li')).toHaveLength(2)
  })

  it('enables confirm only on an exact match and clears the input on close', async () => {
    const { user, onConfirm } = setupExtras()
    await user.click(screen.getByRole('button', { name: 'Remove synthetic-data' }))
    const confirm = await screen.findByRole('button', { name: 'Remove' })
    const input = screen.getByLabelText(/to confirm/)
    expect(confirm).toBeDisabled()
    await user.type(input, 'Synthetic-data')
    expect(confirm).toBeDisabled()
    await user.clear(input)
    await user.type(input, 'synthetic-data ')
    expect(confirm).toBeDisabled()
    await user.clear(input)
    await user.type(input, 'synthetic-data')
    expect(confirm).toBeEnabled()
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
    expect(onConfirm).not.toHaveBeenCalled()
    await user.click(screen.getByRole('button', { name: 'Remove synthetic-data' }))
    expect(await screen.findByLabelText(/to confirm/)).toHaveValue('')
    expect(screen.getByRole('button', { name: 'Remove' })).toBeDisabled()
  })

  it('keeps focus on Cancel, runs confirm after a match, and passes axe', async () => {
    const { user, onConfirm } = setupExtras()
    await user.click(screen.getByRole('button', { name: 'Remove synthetic-data' }))
    const dialog = await screen.findByRole('alertdialog')
    await waitFor(() => expect(screen.getByRole('button', { name: 'Cancel' })).toHaveFocus())
    await expectNoA11yViolations(dialog)
    await user.type(screen.getByLabelText(/to confirm/), 'synthetic-data')
    await user.click(screen.getByRole('button', { name: 'Remove' }))
    expect(onConfirm).toHaveBeenCalledTimes(1)
  })

  it('keeps focus inside the dialog and off the page behind it', async () => {
    const { user } = setupExtras()
    await user.click(screen.getByRole('button', { name: 'Remove synthetic-data' }))
    await screen.findByRole('alertdialog')
    await waitFor(() => expect(screen.getByRole('button', { name: 'Cancel' })).toHaveFocus())
    await user.tab({ shift: true })
    expect(screen.getByLabelText(/to confirm/)).toHaveFocus()
    await user.tab()
    expect(screen.getByRole('button', { name: 'Cancel' })).toHaveFocus()
    // The page behind is inert, so it can never take focus.
    expect(screen.getByRole('button', { name: 'Remove synthetic-data', hidden: true })).not.toHaveFocus()
    expect(screen.queryByRole('button', { name: 'Remove synthetic-data' })).not.toBeInTheDocument()
  })
})
