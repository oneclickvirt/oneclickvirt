import assert from 'node:assert/strict'
import test from 'node:test'

import {
  isMonitorSyncActive,
  isMonitorSyncFailed,
  isMonitorSyncTerminal
} from '../src/view/admin/providers/components/composables/monitorSyncStatus.js'

test('monitor sync polling treats normalized and legacy success statuses as terminal', () => {
  assert.equal(isMonitorSyncTerminal('completed'), true)
  assert.equal(isMonitorSyncTerminal('success'), true)
  assert.equal(isMonitorSyncTerminal('failed'), true)
  assert.equal(isMonitorSyncTerminal('running'), false)
})

test('monitor sync polling only keeps active statuses loading', () => {
  assert.equal(isMonitorSyncActive('pending'), true)
  assert.equal(isMonitorSyncActive(' running '), true)
  assert.equal(isMonitorSyncActive('success'), false)
  assert.equal(isMonitorSyncFailed('failed'), true)
  assert.equal(isMonitorSyncFailed('completed'), false)
})
