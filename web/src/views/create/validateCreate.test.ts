import { describe, expect, it } from 'vitest'
import {
  buildRequest,
  cpuTypeBlocked,
  EMPTY_VALUES,
  fieldOrder,
  isAddress,
  isFieldKey,
  isHostName,
  isReservedName,
  requestRows,
  rosettaBlocked,
  validateCreate,
  type CreateValues,
} from './validateCreate'

const v = (o: Partial<CreateValues>): CreateValues => ({ ...EMPTY_VALUES, name: 'synthetic-a', ...o })

describe('validateCreate', () => {
  it('accepts a bare valid name', () => {
    expect(validateCreate(v({}))).toEqual({})
  })

  it.each(['', '  ', '-a', '.a', 'a b', 'a/b', 'a..b', 'a'.repeat(65)])('rejects the name %j', (name) => {
    expect(validateCreate(v({ name })).name).toBeTruthy()
  })

  it.each(['colima', 'Colima', 'COLIMA-dev', 'colima-'])('rejects the reserved name %s', (name) => {
    expect(isReservedName(name)).toBe(true)
    expect(validateCreate(v({ name })).name).toMatch(/reserved/)
  })

  it.each(['colimadev', 'my-colima', 'default'])('allows %s', (name) => {
    expect(validateCreate(v({ name }))).toEqual({})
  })

  it('rejects non-positive and fractional whole-number fields', () => {
    const e = validateCreate(v({ cpus: '0', diskGiB: '-5', rootDiskGiB: '1.5', memoryGiB: 'x' }))
    expect(Object.keys(e).sort()).toEqual(['cpus', 'diskGiB', 'memoryGiB', 'rootDiskGiB'])
  })

  it('allows fractional memory and caps the maxima', () => {
    expect(validateCreate(v({ memoryGiB: '0.5' })).memoryGiB).toBeTruthy()
    expect(validateCreate(v({ memoryGiB: '1.5' }))).toEqual({})
    expect(validateCreate(v({ memoryGiB: '257' })).memoryGiB).toBeTruthy()
    expect(validateCreate(v({ diskGiB: '8193' })).diskGiB).toBeTruthy()
  })

  it('checks the Kubernetes version only when Kubernetes is on', () => {
    expect(validateCreate(v({ kubernetesVersion: 'junk' }))).toEqual({})
    expect(validateCreate(v({ kubernetes: true, kubernetesVersion: 'junk' })).kubernetesVersion).toBeTruthy()
    expect(validateCreate(v({ kubernetes: true, kubernetesVersion: 'v1.31.2+k3s1' }))).toEqual({})
  })
})

describe('buildRequest', () => {
  it('sends only the name for an untouched form', () => {
    expect(JSON.stringify(buildRequest(v({ name: ' dev ' })))).toBe('{"name":"dev"}')
  })

  it('includes what was filled in and drops the version without Kubernetes', () => {
    expect(
      buildRequest(v({ cpus: '4', memoryGiB: '8', runtime: 'incus', kubernetes: false, kubernetesVersion: 'v1.31.2' })),
    ).toEqual({ name: 'synthetic-a', cpus: 4, memoryGiB: 8, runtime: 'incus' })
  })
})

describe('address and host name checks', () => {
  it.each(['1.1.1.1', '0.0.0.0', '255.255.255.255', '2001:db8::1', '::1', '::', '::ffff:192.0.2.1', '1:2:3:4:5:6:7:8', '1:2:3:4:5:6:7::'])(
    'accepts %s',
    (a) => expect(isAddress(a)).toBe(true),
  )

  it.each([
    '',
    '256.1.1.1',
    '01.1.1.1',
    '1.1.1',
    '1.1.1.1.1',
    '1.1.1.1:53',
    '1.1.1.0/24',
    ' 1.1.1.1',
    'fe80::1%en0',
    '[::1]',
    'example.com',
    '1:2:3:4:5:6:7:8:9',
    '1:2:3:4:5:6:7:8::',
    ':1:2:3:4:5:6:7',
    '1:::2',
    '1::2::3',
    '12345::1',
    'g::1',
    '1.2.3.4::',
  ])('rejects %j', (a) => expect(isAddress(a)).toBe(false))

  it('checks host names label by label', () => {
    for (const n of ['example.com', 'a', 'a-b.c1.test', 'x'.repeat(63)]) expect(isHostName(n)).toBe(true)
    for (const n of ['', 'a..b', 'a.', '.a', '-a.com', 'a-.com', 'a_b.com', 'a b', 'x'.repeat(64), `${'a.'.repeat(127)}a`]) {
      expect(isHostName(n)).toBe(false)
    }
    expect(isHostName(`${'a'.repeat(63)}.${'b'.repeat(63)}.${'c'.repeat(63)}.${'d'.repeat(61)}`)).toBe(true) // 253
    expect(isHostName(`${'a'.repeat(63)}.${'b'.repeat(63)}.${'c'.repeat(63)}.${'d'.repeat(62)}`)).toBe(false) // 254
  })
})

describe('validateCreate, advanced settings', () => {
  it('leaves untouched advanced fields alone', () => {
    expect(validateCreate(v({}))).toEqual({})
  })

  it.each(['-a', 'a-', 'a.b', 'a b', 'a_b', 'a'.repeat(64), 'é'])('rejects the hostname %j', (hostname) => {
    expect(validateCreate(v({ hostname })).hostname).toMatch(/DNS label/)
  })

  it.each(['a', 'dev-vm', 'a'.repeat(63), '1'])('accepts the hostname %s', (hostname) => {
    expect(validateCreate(v({ hostname }))).toEqual({})
  })

  it('names the bad DNS row and skips blank rows', () => {
    const e = validateCreate(v({ dns: ['1.1.1.1', '', 'dns.example', '8.8.8.8:53'] }))
    expect(Object.keys(e).sort()).toEqual(['dns[2]', 'dns[3]'])
  })

  it('names the part of a DNS host row that is wrong', () => {
    const e = validateCreate(
      v({
        dnsHosts: [
          { name: 'ok.test', address: '192.0.2.10' },
          { name: '', address: '' },
          { name: 'bad name', address: '192.0.2.10' },
          { name: 'ok.test', address: 'nope' },
          { name: '', address: '192.0.2.11' },
          { name: 'ok.test', address: '' },
        ],
      }),
    )
    expect(Object.keys(e).sort()).toEqual([
      'dnsHosts[2].name',
      'dnsHosts[3].address',
      'dnsHosts[4].name',
      'dnsHosts[5].address',
    ])
  })

  it('checks the interface only while the mode is bridged', () => {
    expect(validateCreate(v({ networkInterface: 'bad name' }))).toEqual({})
    expect(validateCreate(v({ networkMode: 'bridged', networkInterface: 'bad name' })).networkInterface).toBeTruthy()
    expect(validateCreate(v({ networkMode: 'bridged', networkInterface: 'a'.repeat(16) })).networkInterface).toBeTruthy()
    expect(validateCreate(v({ networkMode: 'bridged', networkInterface: 'en0' }))).toEqual({})
  })

  it('checks the CPU type only for QEMU and refuses a feature list', () => {
    expect(validateCreate(v({ cpuType: 'host,+x2apic' }))).toEqual({})
    expect(validateCreate(v({ vmType: 'qemu', cpuType: 'host,+x2apic' })).cpuType).toBeTruthy()
    expect(validateCreate(v({ vmType: 'qemu', cpuType: 'a'.repeat(65) })).cpuType).toBeTruthy()
    expect(validateCreate(v({ vmType: 'qemu', cpuType: 'cortex-a72' }))).toEqual({})
  })
})

describe('dependent advanced settings', () => {
  it('explains when Rosetta is ruled out', () => {
    expect(rosettaBlocked({ vmType: '', arch: '' })).toBe('')
    expect(rosettaBlocked({ vmType: 'vz', arch: 'aarch64' })).toBe('')
    expect(rosettaBlocked({ vmType: 'qemu', arch: '' })).toMatch(/VZ/)
    expect(rosettaBlocked({ vmType: 'krunkit', arch: '' })).toMatch(/VZ/)
    expect(rosettaBlocked({ vmType: 'vz', arch: 'x86_64' })).toMatch(/aarch64/)
  })

  it('allows a CPU type only with QEMU', () => {
    expect(cpuTypeBlocked({ vmType: 'qemu' })).toBe('')
    expect(cpuTypeBlocked({ vmType: '' })).toMatch(/QEMU/)
    expect(cpuTypeBlocked({ vmType: 'vz' })).toMatch(/QEMU/)
  })
})

describe('buildRequest, advanced settings', () => {
  it('sends every advanced field under its wire key, in a fixed order', () => {
    const body = buildRequest(
      v({
        vmType: 'qemu',
        arch: 'aarch64',
        hostname: ' dev-vm ',
        dns: ['1.1.1.1', '', ' 2001:db8::1 '],
        dnsHosts: [
          { name: ' example.com ', address: '192.0.2.10' },
          { name: '', address: '' },
        ],
        sshAgent: true,
        vzRosetta: true,
        portForwarder: 'grpc',
        mountInotify: true,
        networkAddress: true,
        networkMode: 'bridged',
        networkInterface: ' en0 ',
        cpuType: 'cortex-a72',
      }),
    )
    // vzRosetta is dropped: QEMU rules Rosetta out.
    expect(JSON.stringify(body)).toBe(
      '{"name":"synthetic-a","vmType":"qemu","arch":"aarch64","hostname":"dev-vm","dns":["1.1.1.1","2001:db8::1"],' +
        '"dnsHosts":[{"name":"example.com","address":"192.0.2.10"}],"sshAgent":true,"portForwarder":"grpc",' +
        '"mountInotify":true,"networkAddress":true,"networkMode":"bridged","networkInterface":"en0","cpuType":"cortex-a72"}',
    )
  })

  it('sends Rosetta when it is allowed and drops fields that no longer apply', () => {
    expect(JSON.stringify(buildRequest(v({ vzRosetta: true })))).toBe('{"name":"synthetic-a","vzRosetta":true}')
    const body = buildRequest(v({ networkMode: 'shared', networkInterface: 'en0', cpuType: 'max', vzRosetta: true, arch: 'x86_64' }))
    expect(JSON.stringify(body)).toBe('{"name":"synthetic-a","arch":"x86_64","networkMode":"shared"}')
  })

  it('omits false toggles and empty lists', () => {
    expect(JSON.stringify(buildRequest(v({ dns: ['', ' '], dnsHosts: [{ name: ' ', address: '' }] })))).toBe('{"name":"synthetic-a"}')
  })
})

describe('field keys and order', () => {
  it('knows the keys a 422 can carry', () => {
    for (const k of ['name', 'hostname', 'dns', 'dns[0]', 'dns[12]', 'dnsHosts', 'dnsHosts[3].name', 'dnsHosts[0].address', 'vzRosetta']) {
      expect(isFieldKey(k)).toBe(true)
    }
    for (const k of ['sshAgent', 'dns[x]', 'dnsHosts[0]', 'dnsHosts[0].other', '__proto__', 'kubernetes']) {
      expect(isFieldKey(k)).toBe(false)
    }
  })

  it('lists rows between their group keys', () => {
    const order = fieldOrder(v({ dns: ['', ''], dnsHosts: [{ name: '', address: '' }] }))
    const at = (k: string) => order.indexOf(k)
    expect(at('hostname')).toBeLessThan(at('dns'))
    expect(at('dns')).toBeLessThan(at('dns[0]'))
    expect(at('dns[1]')).toBeLessThan(at('dnsHosts'))
    expect(at('dnsHosts[0].address')).toBeLessThan(at('networkMode'))
    expect(at('kubernetesVersion')).toBeLessThan(at('hostname'))
  })

  it('maps request positions back to form rows', () => {
    expect(
      requestRows(v({ dns: ['', 'a', ' ', 'b'], dnsHosts: [{ name: '', address: '' }, { name: 'x', address: '' }] })),
    ).toEqual({ dns: [1, 3], dnsHosts: [1] })
  })
})
