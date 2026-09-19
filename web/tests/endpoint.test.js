import test from 'node:test'
import assert from 'node:assert/strict'

import {
  extractEndpointHost,
  formatEndpointHostForUrl,
  formatEndpointHostPort
} from '../src/utils/endpoint.js'

test('formats IPv4 and host names without changing their endpoint syntax', () => {
  assert.equal(formatEndpointHostForUrl('192.0.2.10'), '192.0.2.10')
  assert.equal(formatEndpointHostPort('example.test', 8080), 'example.test:8080')
})

test('brackets raw and already bracketed IPv6 hosts for endpoint display', () => {
  assert.equal(extractEndpointHost('[2001:db8::10]:22'), '2001:db8::10')
  assert.equal(formatEndpointHostForUrl('2001:db8::10'), '[2001:db8::10]')
  assert.equal(formatEndpointHostPort('2001:db8::10', 8080), '[2001:db8::10]:8080')
  assert.equal(formatEndpointHostPort('[2001:db8::10]', 8080), '[2001:db8::10]:8080')
})

test('normalizes bracketed IPv6 hosts returned by URL parsing', () => {
  assert.equal(extractEndpointHost('ssh://[2001:db8::1]:22'), '2001:db8::1')
  assert.equal(formatEndpointHostPort('ssh://[2001:db8::1]:22', 2222), '[2001:db8::1]:2222')
})

test('formats domain proxy targets for IPv6 and IPv4 consistently', () => {
  assert.equal(formatEndpointHostPort('2001:db8::20', 8080), '[2001:db8::20]:8080')
  assert.equal(formatEndpointHostPort('192.0.2.20', 8080), '192.0.2.20:8080')
})

test('does not append a separator when no port was supplied', () => {
  assert.equal(formatEndpointHostPort('2001:db8::10'), '[2001:db8::10]')
  assert.equal(formatEndpointHostPort('', 8080), '')
})
