import test from 'node:test'
import assert from 'node:assert/strict'

import { requiresRoutedStaticIPv6Provider, supportsIPv6OnlyProvider, supportsStaticIPv6Provider } from '../src/utils/ipv6Capabilities.js'

test('static IPv6 capability matrix includes every routed production backend', () => {
  for (const providerType of ['lxd', 'incus', 'proxmox', 'proxmoxve', 'docker', 'podman', 'containerd', 'orbstack', 'qemu', 'kubevirt', 'vmware', 'virtualbox', 'multipass', 'vagrant']) {
    assert.equal(supportsStaticIPv6Provider(providerType), true, providerType)
  }
  for (const providerType of ['unknown', 'hyperv', 'xen']) {
    assert.equal(supportsStaticIPv6Provider(providerType), false, providerType)
  }
})

test('only bridge-capable VM backends require a tunnel-managed static pool', () => {
  assert.equal(requiresRoutedStaticIPv6Provider(' QEMU '), true)
  assert.equal(requiresRoutedStaticIPv6Provider('kubevirt'), true)
  assert.equal(requiresRoutedStaticIPv6Provider('vmware'), true)
  assert.equal(requiresRoutedStaticIPv6Provider('virtualbox'), true)
  assert.equal(requiresRoutedStaticIPv6Provider('multipass'), true)
  assert.equal(requiresRoutedStaticIPv6Provider('vagrant'), true)
  assert.equal(requiresRoutedStaticIPv6Provider('proxmox'), false)
})

test('IPv6-only capability keeps provider management network limits explicit', () => {
  assert.equal(supportsIPv6OnlyProvider('multipass'), false)
  assert.equal(supportsIPv6OnlyProvider('vagrant'), false)
  assert.equal(supportsIPv6OnlyProvider('virtualbox'), true)
  assert.equal(supportsIPv6OnlyProvider('proxmox'), true)
})
