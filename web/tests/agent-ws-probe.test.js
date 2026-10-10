import test from 'node:test'
import assert from 'node:assert/strict'

import { extractAgentWsURL, agentWsProbeURL, replaceAgentWsProtocol, startAgentWsProbe } from '../src/view/admin/providers/components/formTabs/composables/agentWsProbeCore.js'

test('probe follows the real agent path without installation secrets', () => {
  const url = extractAgentWsURL("sh --ws-url 'ws://controller:8888/api/v1/ws/agent?secret=private' --secret private")
  assert.equal(agentWsProbeURL(url, 'wss:'), 'wss://controller:8888/api/v1/ws/agent?probe=1')
  assert.equal(extractAgentWsURL('not a command'), null)
})

test('command protocol replacement handles quoted and unquoted generated commands', () => {
  const command = "curl script | sh -s -- --ws-url 'wss://controller/api/v1/ws/agent' --secret secret"
  assert.equal(replaceAgentWsProtocol(command, 'ws'), "curl script | sh -s -- --ws-url 'ws://controller/api/v1/ws/agent' --secret secret")
  assert.equal(replaceAgentWsProtocol('--ws-url wss://controller/path', 'ws'), '--ws-url ws://controller/path')
  assert.equal(replaceAgentWsProtocol(command, 'invalid'), command)
})

test('successful handshake closes the probe', async () => {
  let socket
  class Socket { constructor(url) { this.url = url; socket = this } close() { this.closed = true } }
  const probe = startAgentWsProbe('wss://controller/api/v1/ws/agent?probe=1', { Socket })
  socket.onopen()
  assert.equal(await probe.promise, true)
  assert.equal(socket.closed, true)
  assert.equal(socket.onerror, null)
})

test('failed and cancelled probes release sockets and ignore late events', async () => {
  const sockets = []
  class Socket { constructor() { sockets.push(this) } close() { this.closed = true } }
  const old = startAgentWsProbe('ws://old', { Socket })
  const late = sockets[0].onopen
  old.cancel()
  const current = startAgentWsProbe('wss://new', { Socket })
  late()
  sockets[1].onerror()
  assert.equal(await old.promise, false)
  assert.equal(await current.promise, false)
  assert.ok(sockets.every(s => s.closed))
})

test('a blocked handshake times out without leaving an open socket', async () => {
  let closed = false
  class Socket { close() { closed = true } }
  const probe = startAgentWsProbe('ws://controller', { Socket, timeoutMs: 10 })
  assert.equal(await probe.promise, false)
  assert.equal(closed, true)
})
