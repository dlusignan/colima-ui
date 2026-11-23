import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { profileFixture } from '@/test/fixtures'
import { WarningBanners } from './WarningBanners'

describe('WarningBanners', () => {
  it('renders nothing without warnings', () => {
    const { container } = render(<WarningBanners profiles={[profileFixture()]} />)
    expect(container).toBeEmptyDOMElement()
  })

  it('dedupes the unavailable banner and the version banner per version', () => {
    const profiles = [
      profileFixture({
        name: 'a',
        colimaVersion: '0.9.1',
        warnings: [
          { code: 'colima_unavailable', message: 'down' },
          { code: 'colima_version_unsupported', message: 'Version 0.9.1 is unsupported.' },
        ],
      }),
      profileFixture({
        name: 'b',
        colimaVersion: '0.9.1',
        warnings: [
          { code: 'colima_unavailable', message: 'down' },
          { code: 'colima_version_unsupported', message: 'Version 0.9.1 is unsupported.' },
        ],
      }),
    ]
    render(<WarningBanners profiles={profiles} />)
    expect(screen.getAllByRole('status')).toHaveLength(2)
    expect(screen.getByText(/Showing configuration only/)).toBeInTheDocument()
    expect(screen.getByText('Version 0.9.1 is unsupported.')).toBeInTheDocument()
  })
})
