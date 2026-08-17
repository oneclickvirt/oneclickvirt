import test from 'node:test'
import assert from 'node:assert/strict'

import { canProbeWssEndpoint } from '../src/view/admin/providers/components/formTabs/composables/agentWsProbeCore.js'

test('does not anonymously probe the authenticated agent websocket endpoint', () => {
  assert.equal(canProbeWssEndpoint({ path: '/api/v1/ws/agent' }), false)
  assert.equal(canProbeWssEndpoint({ path: '/ws/agent/' }), false)
})

test('allows an anonymous WSS probe for unrelated endpoints', () => {
  assert.equal(canProbeWssEndpoint({ path: '/health/socket' }), true)
})
