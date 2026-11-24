import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { RowActions } from './RowActions'
import { SectionToolbar } from './SectionToolbar'
import { expectNoA11yViolations } from '@/test/axe'

describe('RowActions', () => {
  it('names every control after the object and works from the keyboard', async () => {
    const onInspect = vi.fn()
    const user = userEvent.setup()
    render(<RowActions object="synthetic-data" actions={[{ id: 'inspect', label: 'Inspect', onSelect: onInspect }]} />)
    expect(screen.getByRole('group', { name: 'Actions for synthetic-data' })).toBeInTheDocument()
    await user.tab()
    expect(screen.getByRole('button', { name: 'Inspect synthetic-data' })).toHaveFocus()
    await user.keyboard('{Enter}')
    await user.keyboard(' ')
    expect(onInspect).toHaveBeenCalledTimes(2)
  })

  it('keeps a disabled action focusable with a visible reason and does nothing on activation', async () => {
    const onSelect = vi.fn()
    const user = userEvent.setup()
    const { container } = render(
      <RowActions
        object="synthetic-data"
        actions={[
          {
            id: 'remove',
            label: 'Remove',
            onSelect,
            disabledReason: 'In use by 2 containers.',
            confirm: { title: 'Remove synthetic-data?', description: 'Gone for good.', confirmLabel: 'Remove', destructive: true },
          },
        ]}
      />,
    )
    const button = screen.getByRole('button', { name: 'Remove synthetic-data' })
    expect(button).toHaveAttribute('aria-disabled', 'true')
    expect(screen.getByText('In use by 2 containers.')).toBeVisible()
    expect(button).toHaveAccessibleDescription('In use by 2 containers.')
    await user.tab()
    expect(button).toHaveFocus()
    await user.keyboard('{Enter}')
    await user.click(button)
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(onSelect).not.toHaveBeenCalled()
    await expectNoA11yViolations(container)
  })

  it('confirms before running a destructive action', async () => {
    const onSelect = vi.fn()
    const user = userEvent.setup()
    render(
      <RowActions
        object="synthetic-data"
        actions={[
          {
            id: 'remove',
            label: 'Remove',
            onSelect,
            confirm: { title: 'Remove synthetic-data?', description: 'Gone for good.', confirmLabel: 'Remove volume', destructive: true },
          },
        ]}
      />,
    )
    await user.click(screen.getByRole('button', { name: 'Remove synthetic-data' }))
    expect(onSelect).not.toHaveBeenCalled()
    await user.click(await screen.findByRole('button', { name: 'Remove volume' }))
    expect(onSelect).toHaveBeenCalledTimes(1)
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
  })

  it('disables a pending action', () => {
    render(<RowActions object="synthetic-data" actions={[{ id: 'a', label: 'Remove', onSelect: vi.fn(), pending: true }]} />)
    expect(screen.getByRole('button', { name: 'Remove synthetic-data' })).toHaveAttribute('aria-disabled', 'true')
  })

  it('renders nothing without actions', () => {
    const { container } = render(<RowActions object="x" actions={[]} />)
    expect(container).toBeEmptyDOMElement()
  })
})

describe('SectionToolbar', () => {
  it('renders nothing without actions', () => {
    const { container } = render(<SectionToolbar label="Volume actions" actions={[]} />)
    expect(container).toBeEmptyDOMElement()
  })

  it('runs actions and passes axe', async () => {
    const onCreate = vi.fn()
    const user = userEvent.setup()
    const { container } = render(
      <SectionToolbar label="Volume actions" actions={[{ id: 'create', label: 'Create volume', onSelect: onCreate }]} />,
    )
    await user.click(screen.getByRole('button', { name: 'Create volume' }))
    expect(onCreate).toHaveBeenCalledTimes(1)
    await expectNoA11yViolations(container)
  })

  it('shows one shared reason and disables every action when the profile is not running', async () => {
    const onCreate = vi.fn()
    const user = userEvent.setup()
    render(
      <SectionToolbar
        label="Volume actions"
        disabledReason="Start the profile to use these actions."
        actions={[
          { id: 'create', label: 'Create volume', onSelect: onCreate },
          { id: 'prune', label: 'Prune volumes', onSelect: vi.fn() },
        ]}
      />,
    )
    expect(screen.getAllByText('Start the profile to use these actions.')).toHaveLength(1)
    for (const name of ['Create volume', 'Prune volumes']) {
      const b = screen.getByRole('button', { name })
      expect(b).toHaveAttribute('aria-disabled', 'true')
      expect(b).toHaveAccessibleDescription('Start the profile to use these actions.')
    }
    await user.click(screen.getByRole('button', { name: 'Create volume' }))
    expect(onCreate).not.toHaveBeenCalled()
  })
})
