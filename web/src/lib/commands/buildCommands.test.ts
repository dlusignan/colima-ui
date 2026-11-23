import { describe, expect, it } from 'vitest'
import { profileRoutes } from '@/app/profileRoutes'
import { themes } from '@/lib/theme'
import { profileFixture } from '@/test/fixtures'
import { buildCommands } from './buildCommands'

const build = (profiles = [profileFixture({ name: 'a' })], extra = {}) =>
  buildCommands({ profiles, busy: new Set(), themes, tabs: profileRoutes, ...extra })
const labels = (cs: ReturnType<typeof build>) => cs.map((c) => c.label)

describe('buildCommands', () => {
  it('lists navigation, themes and help', () => {
    const l = labels(build())
    expect(l).toContain('Go to Dashboard')
    for (const tab of profileRoutes) expect(l).toContain(`Go to a: ${tab.label}`)
    expect(l).toContain('Theme: System')
    for (const t of themes) expect(l).toContain(`Theme: ${t.label}`)
    expect(l).toContain('Keyboard shortcuts')
    expect(new Set(l).size).toBe(l.length)
  })

  it('offers stop and restart for a running profile, not start', () => {
    const l = labels(build())
    expect(l).toContain('Stop a')
    expect(l).toContain('Restart a')
    expect(l).not.toContain('Start a')
  })

  it('offers start and restart for a stopped profile, not stop', () => {
    const l = labels(build([profileFixture({ name: 'a', status: 'stopped' })]))
    expect(l).toContain('Start a')
    expect(l).toContain('Restart a')
    expect(l).not.toContain('Stop a')
  })

  it('hides every action while busy or when Colima is unreachable', () => {
    const noActions = (cs: ReturnType<typeof build>) => cs.filter((c) => c.group === 'Actions')
    expect(noActions(build(undefined, { busy: new Set(['a']) }))).toEqual([])
    expect(noActions(build([profileFixture({ name: 'a', activeJobId: 'j1' })]))).toEqual([])
    const unreachable = profileFixture({ name: 'a', warnings: [{ code: 'colima_unavailable', message: 'x' }] })
    expect(noActions(build([unreachable]))).toEqual([])
  })

  it('encodes profile names in paths and puts the current profile first', () => {
    const cs = build([profileFixture({ name: 'a' }), profileFixture({ name: 'b/c' })], { currentProfile: 'b/c' })
    const nav = cs.filter((c) => c.intent.type === 'navigate')
    expect(nav[1].label.startsWith('Go to b/c')).toBe(true)
    expect(nav[1].intent).toEqual({ type: 'navigate', to: '/profiles/b%2Fc' })
    expect(cs.find((c) => c.label === 'Stop b/c')).toBeDefined()
    expect(cs.filter((c) => c.group === 'Actions')[0].label).toBe('Stop b/c')
  })
})
