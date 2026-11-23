import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it } from 'vitest'
import App from '@/App'

describe('App', () => {
  it('renders the placeholder page with a shadcn button', () => {
    render(
      <MemoryRouter initialEntries={['/']}>
        <App />
      </MemoryRouter>,
    )
    expect(
      screen.getByRole('heading', { name: /colima ui/i }),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: /get started/i }),
    ).toBeInTheDocument()
  })
})
