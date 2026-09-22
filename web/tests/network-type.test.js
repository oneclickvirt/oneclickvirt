import test from 'node:test'
import assert from 'node:assert/strict'

import {
  hasAgentMappedNetworking,
  resolveInstanceNetworkType,
  usesControllerIPv6Pool,
  usesManagedIPv6NAT
} from '../src/utils/networkType.js'

test('Agent NAT supports controller forwarding without a node public IP', () => {
  assert.equal(hasAgentMappedNetworking({
    connectionType: 'agent',
    networkType: 'nat_ipv4',
    portIP: ''
  }), true)
  assert.equal(hasAgentMappedNetworking({
    connectionType: 'agent',
    networkType: 'no_port_mapping',
    portIP: '192.0.2.10'
  }), false)
  assert.equal(hasAgentMappedNetworking({ connectionType: 'ssh', networkType: 'nat_ipv4' }), false)
})

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
    assert.equal(usesManagedIPv6NAT(provider, ' NAT_IPV4_IPV6 ', 'device_proxy'), true)
    assert.equal(usesManagedIPv6NAT(provider, ' NAT_IPV4_IPV6 ', 'iptables'), true)
    assert.equal(usesControllerIPv6Pool(provider, 'nat_ipv4_ipv6', 'device_proxy'), false)
    assert.equal(usesControllerIPv6Pool(provider, 'nat_ipv4_ipv6', 'iptables'), false)
    assert.equal(usesControllerIPv6Pool(provider, 'dedicated_ipv4_ipv6'), true)
    assert.equal(usesControllerIPv6Pool(provider, 'ipv6_only'), true)
  }
})

test('Incus and LXD native dual stack consumes a public guest allocation', () => {
  for (const provider of ['incus', ' LXD ']) {
    assert.equal(usesManagedIPv6NAT(provider, 'nat_ipv4_ipv6', ' NATIVE '), false)
    assert.equal(usesControllerIPv6Pool(provider, 'nat_ipv4_ipv6', ' NATIVE '), true)
  }
})

test('other routed IPv6 backends retain controller pool allocation', () => {
  for (const provider of ['qemu', 'proxmox', 'vmware']) {
    assert.equal(usesManagedIPv6NAT(provider, 'nat_ipv4_ipv6', 'native'), false)
    assert.equal(usesControllerIPv6Pool(provider, 'nat_ipv4_ipv6', 'native'), true)
  }
  assert.equal(usesControllerIPv6Pool('incus', 'nat_ipv4'), false)
})
