import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { expectNoA11yViolations } from '@/test/axe'
import { StatusDot } from './StatusDot'

describe('StatusDot', () => {
  it.each([
    ['running', 'bg-success'],
    ['stopped', 'bg-muted-foreground'],
    ['unknown', 'bg-warning'],
  ] as const)('shows %s as text with a token colour', (status, cls) => {
    const { container } = render(<StatusDot status={status} />)
    expect(screen.getByText(status)).toBeInTheDocument()
    const dot = container.querySelector('[aria-hidden="true"]')
    expect(dot).toHaveClass(cls)
  })

  it('shows a transient label instead of the status', () => {
    render(<StatusDot status="stopped" label="starting" />)
    expect(screen.getByText('starting')).toBeInTheDocument()
    expect(screen.queryByText('stopped')).not.toBeInTheDocument()
  })

  it('passes the axe check inside a table', async () => {
    const { container } = render(
      <table>
        <caption>Profiles</caption>
        <thead>
          <tr>
            <th scope="col">Status</th>
          </tr>
        </thead>
        <tbody>
          <tr>
            <td>
              <StatusDot status="running" />
            </td>
          </tr>
        </tbody>
      </table>,
    )
    await expectNoA11yViolations(container)
  })

  it('the axe helper reports a violation', async () => {
    const { container } = render(<img src="x.png" />)
    await expect(expectNoA11yViolations(container)).rejects.toThrow(/image-alt/)
  })
})
