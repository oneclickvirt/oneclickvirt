import test from 'node:test'
import assert from 'node:assert/strict'

import { canSyncProviderInstances } from '../src/utils/providerDiscovery.js'

test('instance sync is only enabled for providers recorded as non-clean nodes', () => {
  assert.equal(canSyncProviderInstances({ instanceDiscoveryEnabled: true }), true)
  assert.equal(canSyncProviderInstances({ instanceDiscoveryEnabled: false }), false)
  assert.equal(canSyncProviderInstances({ pendingDiscovery: true }), false)
  assert.equal(canSyncProviderInstances(null), false)
})
