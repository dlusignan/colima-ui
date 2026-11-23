import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { ErrorBoundary } from './ErrorBoundary'

function Boom({ explode }: { explode: boolean }) {
  if (explode) throw new Error('kaput')
  return <p>fine</p>
}

describe('ErrorBoundary', () => {
  it('shows a fallback with the message when a child throws', () => {
    vi.spyOn(console, 'error').mockImplementation(() => undefined)
    render(
      <ErrorBoundary>
        <Boom explode />
      </ErrorBoundary>,
    )
    const alert = screen.getByRole('alert')
    expect(alert).toHaveTextContent('Something went wrong')
    expect(alert).toHaveTextContent('kaput')
  })

  it('renders children when nothing throws', () => {
    render(
      <ErrorBoundary>
        <Boom explode={false} />
      </ErrorBoundary>,
    )
    expect(screen.getByText('fine')).toBeInTheDocument()
  })

  it('retry re-renders the children', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => undefined)
    function Harness() {
      const [explode, setExplode] = useState(true)
      return (
        <>
          <button onClick={() => setExplode(false)}>heal</button>
          <ErrorBoundary>
            <Boom explode={explode} />
          </ErrorBoundary>
        </>
      )
    }
    const user = userEvent.setup()
    render(<Harness />)
    await user.click(screen.getByRole('button', { name: 'heal' }))
    await user.click(screen.getByRole('button', { name: 'Try again' }))
    expect(screen.getByText('fine')).toBeInTheDocument()
  })
})
