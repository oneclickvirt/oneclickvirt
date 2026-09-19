import test from 'node:test'
import assert from 'node:assert/strict'

import {
  resolveInstanceNetworkType,
  usesControllerIPv6Pool,
  usesManagedIPv6NAT
} from '../src/utils/networkType.js'

test('instance network type wins over provider fallback', () => {
  assert.equal(
    resolveInstanceNetworkType(
      { networkType: ' NAT_IPV4 ', providerId: 2 },
      [{ id: 2, networkType: 'nat_ipv4_ipv6' }]
    ),
    'nat_ipv4'
  )
})

test('legacy instance without a network snapshot inherits provider dual stack', () => {
  assert.equal(
    resolveInstanceNetworkType(
      { networkType: '', providerId: 2 },
      [{ id: 2, networkType: ' NAT_IPV4_IPV6 ' }]
    ),
    'nat_ipv4_ipv6'
  )
})

test('missing provider configuration remains empty instead of guessing IPv6', () => {
  assert.equal(resolveInstanceNetworkType({ providerId: 99 }, []), '')
})

test('Incus and LXD managed IPv6 NAT never consume the routed address pool', () => {
  for (const provider of ['incus', ' LXD ']) {
    assert.equal(usesManagedIPv6NAT(provider, ' NAT_IPV4_IPV6 '), true)
    assert.equal(usesControllerIPv6Pool(provider, 'nat_ipv4_ipv6'), false)
    assert.equal(usesControllerIPv6Pool(provider, 'dedicated_ipv4_ipv6'), true)
    assert.equal(usesControllerIPv6Pool(provider, 'ipv6_only'), true)
  }
})

test('other routed IPv6 backends retain controller pool allocation', () => {
  for (const provider of ['qemu', 'proxmox', 'vmware']) {
    assert.equal(usesManagedIPv6NAT(provider, 'nat_ipv4_ipv6'), false)
    assert.equal(usesControllerIPv6Pool(provider, 'nat_ipv4_ipv6'), true)
  }
  assert.equal(usesControllerIPv6Pool('incus', 'nat_ipv4'), false)
})
