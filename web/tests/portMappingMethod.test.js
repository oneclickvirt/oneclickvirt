import test from 'node:test'
import assert from 'node:assert/strict'
import { normalizePortMappingMethods } from '../src/utils/portMappingMethod.js'

test('PVE async form load and resubmit repair legacy device_proxy in every network mode', () => {
  for (const network of ['nat_ipv4', 'nat_ipv4_ipv6', 'dedicated_ipv4', 'dedicated_ipv4_ipv6', 'ipv6_only']) {
    const repaired = normalizePortMappingMethods('proxmox', network, 'device_proxy', 'device_proxy')
    assert.equal(repaired.ipv4PortMappingMethod, network.startsWith('nat_') ? 'iptables' : 'native')
    assert.equal(repaired.ipv6PortMappingMethod, 'native')
    assert.deepEqual(normalizePortMappingMethods('proxmox', network, repaired.ipv4PortMappingMethod, repaired.ipv6PortMappingMethod), repaired)
  }
})

test('explicit IPv6 mapping and Incus proxy choices survive normalization', () => {
  assert.equal(normalizePortMappingMethods('proxmox', 'nat_ipv4_ipv6', 'native', 'iptables').ipv6PortMappingMethod, 'iptables')
  assert.deepEqual(normalizePortMappingMethods('incus', 'nat_ipv4_ipv6', 'device_proxy', 'device_proxy'), {
    ipv4PortMappingMethod: 'device_proxy', ipv6PortMappingMethod: 'device_proxy'
  })
})
