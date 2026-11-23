import { describe, expect, it } from 'vitest'
import { containerHealth, formatPorts } from './containerStatus'

describe('containerHealth', () => {
  it.each([
    ['Exited (0) 3 days ago', 'exited'],
    ['Exited (137) 2 hours ago', 'crashed'],
    ['Up 5 minutes', 'running'],
    ['Created', 'other'],
    ['Paused', 'other'],
    ['Restarting (1) 4 seconds ago', 'other'],
    ['', 'other'],
  ])('%j is %s', (status, want) => {
    expect(containerHealth(status)).toBe(want)
  })

  it.each([
    ['Up 5 minutes (Paused)', 'paused', 'paused'],
    ['Up 5 minutes', 'paused', 'paused'],
    ['Up 5 minutes', 'running', 'running'],
    ['Exited (137) 2 hours ago', 'exited', 'crashed'],
  ])('%j with state %s is %s', (status, state, want) => {
    expect(containerHealth(status, state)).toBe(want)
  })
})

describe('formatPorts', () => {
  it('formats published, exposed-only and deduplicated ports', () => {
    expect(
      formatPorts([
        { containerPort: 80, protocol: 'tcp', hostIp: '0.0.0.0', hostPort: 8080 },
        { containerPort: 80, protocol: 'tcp', hostIp: '::', hostPort: 8080 },
        { containerPort: 5432, protocol: 'tcp', hostIp: null, hostPort: null },
      ]),
    ).toEqual(['8080:80/tcp', '5432/tcp'])
  })
  it('is empty without ports', () => {
    expect(formatPorts([])).toEqual([])
  })
})
