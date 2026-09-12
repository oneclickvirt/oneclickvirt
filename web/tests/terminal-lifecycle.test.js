import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import vm from 'node:vm'

test('popup terminal reconnect binds input once and ignores stale socket events', () => {
  const source = readFileSync(new URL('../src/components/GlobalSSHManager.vue', import.meta.url), 'utf8')
  const start = source.indexOf('function connectWebSocket()')
  const end = source.indexOf('// 重连函数', start)
  assert.ok(start > 0 && end > start)
  const sockets = []
  const inputHandlers = []
  const writes = []
  let heartbeats = 0
  class Socket {
    static OPEN = 1
    constructor() { this.readyState = 1; this.sent = []; sockets.push(this) }
    send(data) { this.sent.push(data) }
  }
  const context = {
    WebSocket: Socket, websocket: null, isIntentionallyClosed: false,
    ArrayBuffer, Uint8Array, JSON,
    terminal: { cols: 80, rows: 24, writeln: data => writes.push(data), write: data => writes.push(data),
      focus() {}, onData: handler => inputHandlers.push(handler) },
    startHeartbeat: () => { heartbeats++ }, stopHeartbeat: () => { heartbeats-- },
    setTimeout: () => { throw new Error('stale callback scheduled reconnect') }
  }
  vm.createContext(context)
  vm.runInContext(source.slice(start, end), context)
  for (let i = 0; i < 3; i++) vm.runInContext('connectWebSocket()', context)
  assert.equal(inputHandlers.length, 1)
  inputHandlers[0]('one keystroke')
  assert.deepEqual(sockets.map(socket => socket.sent.length), [0, 0, 1])
  const before = writes.length
  for (const socket of sockets.slice(0, 2)) {
    socket.onopen()
    socket.onmessage({ data: 'stale output' })
    socket.onerror()
    socket.onclose({ code: 1006 })
  }
  assert.equal(writes.length, before)
  assert.equal(heartbeats, 0)
  sockets[2].onmessage({ data: 'current output' })
  assert.equal(writes.at(-1), 'current output')
})
