import { describe, expect, it } from 'vitest'
import { buildCreateRequest, validateNetworkName, validateSubnet } from './networkRules'

describe('validateSubnet', () => {
  it.each([
    '',
    '   ',
    '172.28.0.0/16',
    '172.28.0.5/16',
    '10.0.0.0/8',
    '10.1.2.0/28',
    '192.168.0.0/24',
    '192.168.6.0/24',
    '172.16.0.0/12',
    ' 172.28.0.0/16 ',
    'fd00:1234:5678::/48',
    'fd00:1234:5678:1::/64',
    'fd00::1234/120',
    'fd00::1.2.3.4/120',
  ])('accepts %j', (v) => expect(validateSubnet(v)).toBeUndefined())

  it.each([
    ['172.28.0.0', 'CIDR form'],
    ['172.28.0.0/', 'CIDR form'],
    ['/16', 'CIDR form'],
    ['172.28.0/16', 'CIDR form'],
    ['172.28.0.256/16', 'CIDR form'],
    ['172.028.0.0/16', 'CIDR form'],
    ['172.28.0.0/016', 'CIDR form'],
    ['172.28.0.0/+16', 'CIDR form'],
    ['172.28.0.0/16/8', 'CIDR form'],
    ['172.28.0.0/16,10.0.0.0/8', 'CIDR form'],
    ['172.28.0.0 /16', 'no spaces'],
    ['fe80::1%eth0/64', 'CIDR form'],
    ['fd00:::/64', 'CIDR form'],
    ['fd00::g/64', 'CIDR form'],
    ['1:2:3:4:5:6:7:8:9/64', 'CIDR form'],
    ['172.28.0.0/7', 'between /8 and /28'],
    ['172.28.0.0/29', 'between /8 and /28'],
    ['0.0.0.0/0', 'between /8 and /28'],
    ['fd00::/47', 'between /48 and /120'],
    ['fd00::/121', 'between /48 and /120'],
    ['fd00::/128', 'between /48 and /120'],
    ['::ffff:10.0.0.0/120', 'IPv4-mapped'],
    ['::ffff:a00:0/120', 'IPv4-mapped'],
    ['127.0.0.0/8', 'loopback'],
    ['127.1.0.0/16', 'loopback'],
    ['0.0.0.0/8', 'unspecified'],
    ['0.1.0.0/16', 'unspecified'],
    ['169.254.0.0/16', 'link-local'],
    ['169.254.9.0/24', 'link-local'],
    ['224.0.0.0/8', 'multicast'],
    ['239.1.0.0/16', 'multicast'],
    ['240.0.0.0/8', 'reserved'],
    ['250.0.0.0/8', 'reserved'],
    ['192.168.5.0/24', 'Colima VM'],
    ['192.168.5.128/28', 'Colima VM'],
    ['192.168.0.0/16', 'Colima VM'],
    ['192.0.0.0/8', 'Colima VM'],
    ['fe80::/64', 'link-local'],
    ['ff02::/64', 'multicast'],
    ['::1/120', 'loopback'],
    ['::/48', 'overlaps'],
    ['0:1::/48', 'unspecified'],
  ])('refuses %j', (v, part) => {
    const message = validateSubnet(v)
    expect(message).toBeDefined()
    expect(message).toContain(part)
  })

  it('refuses text over 64 characters', () => {
    expect(validateSubnet(`fd00::${'1'.repeat(70)}/64`)).toContain('at most 64')
  })

  it('trims the ends before checking', () => {
    expect(validateSubnet('172.28.0.0/16\n')).toBeUndefined()
  })

  it('refuses control and non-ASCII characters', () => {
    expect(validateSubnet('172.28.0.0/16​')).toBeDefined()
    expect(validateSubnet('172.28.0.0\u0000/16')).toBeDefined()
  })
})

describe('validateNetworkName', () => {
  it.each(['backend', 'a', 'my-net_1.x', 'A'.repeat(128)])('accepts %j', (n) => expect(validateNetworkName(n)).toBeUndefined())
  it.each(['', ' x', 'x y', '.x', '-x', '_x', 'a/b', 'a'.repeat(129), 'né'])('refuses %j', (n) =>
    expect(validateNetworkName(n)).toBeDefined(),
  )
  it.each(['bridge', 'HOST', 'None', 'Ingress'])('refuses the reserved name %j in any case', (n) =>
    expect(validateNetworkName(n)).toContain('built into Docker'),
  )
  it('allows names that merely contain a reserved word', () => {
    expect(validateNetworkName('bridge2')).toBeUndefined()
    expect(validateNetworkName('my-host')).toBeUndefined()
  })
})

describe('buildCreateRequest', () => {
  it('omits the subnet when empty and keeps the text as typed otherwise', () => {
    expect(JSON.stringify(buildCreateRequest('a', false, ' '))).toBe('{"name":"a","internal":false}')
    expect(JSON.stringify(buildCreateRequest('a', true, ' 172.28.0.5/16 '))).toBe(
      '{"name":"a","internal":true,"subnet":"172.28.0.5/16"}',
    )
  })
})
