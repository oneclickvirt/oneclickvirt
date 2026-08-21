import test from 'node:test'
import assert from 'node:assert/strict'
import { normalizeConsoleCapabilities } from '../src/utils/consoleCapabilities.js'

test('adds VNC advertised by a legacy protocol list without auto-selecting it', () => {
  const capabilities = normalizeConsoleCapabilities({
    available: true,
    capabilities: [{ protocol: 'serial', available: true, terminal: true }],
    protocols: ['serial', ' VNC ']
  }, 'unknown')

  assert.deepEqual(capabilities.map(item => item.protocol), ['serial', 'vnc'])
  assert.equal(capabilities[1].available, true)
  assert.equal(capabilities[1].terminal, false)
})

test('supports the older single-protocol console response', () => {
  const capabilities = normalizeConsoleCapabilities({
    protocol: 'VNC',
    available: true
  }, 'unknown')

  assert.deepEqual(capabilities, [{
    protocol: 'vnc',
    available: true,
    repairable: false,
    repairStatus: '',
    reason: '',
    nativeURL: '',
    terminal: false
  }])
})

test('does not promote an explicitly unavailable VNC capability from aggregate availability', () => {
  const capabilities = normalizeConsoleCapabilities({
    available: true,
    protocols: ['serial', 'vnc'],
    capabilities: [
      { protocol: 'serial', available: true, terminal: true },
      { protocol: 'vnc', available: false, terminal: false, reason: 'not configured' }
    ]
  }, 'unknown')

  assert.equal(capabilities[1].available, false)
  assert.equal(capabilities[1].reason, 'not configured')
})

test('uses an unsupported fallback when the controller returns no protocol data', () => {
  const capabilities = normalizeConsoleCapabilities({ reason: 'unavailable' }, 'unknown')
  assert.deepEqual(capabilities, [{
    protocol: 'unsupported',
    available: false,
    repairable: false,
    reason: 'unavailable'
  }])
})

test('retains a live Telnet capability as an explicit native-client choice', () => {
  const capabilities = normalizeConsoleCapabilities({
    capabilities: [{ protocol: 'telnet', available: true, nativeURL: 'telnet://node.example.test:2323', terminal: false }]
  }, 'unknown')

  assert.equal(capabilities.length, 1)
  assert.equal(capabilities[0].protocol, 'telnet')
  assert.equal(capabilities[0].nativeURL, 'telnet://node.example.test:2323')
})
