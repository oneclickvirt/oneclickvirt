import test from 'node:test'
import assert from 'node:assert/strict'
import { createSerialConsoleInputFilter } from '../src/utils/serialConsoleInput.js'

test('serial input suppresses a complete xterm cursor-position reply', () => {
  const filter = createSerialConsoleInputFilter()
  assert.equal(filter.filter('\x1b[29;99Rpassword\r'), 'password\r')
})

test('serial input suppresses repeated cursor-position replies', () => {
  const filter = createSerialConsoleInputFilter()
  assert.equal(filter.filter('\x1b[29;99R'), '')
  assert.equal(filter.filter('\x1b[24;80Rpassword\r'), 'password\r')
})

test('serial input suppresses a cursor-position reply split across callbacks', () => {
  const filter = createSerialConsoleInputFilter()
  assert.equal(filter.filter('\x1b[29;'), '')
  assert.equal(filter.filter('99Rpassword\r'), 'password\r')
})

test('serial input suppresses a cursor-position reply split after Escape', () => {
  const filter = createSerialConsoleInputFilter()
  assert.equal(filter.filter('\x1b'), '')
  assert.equal(filter.hasPending(), true)
  assert.equal(filter.filter('[29;99Rpassword\r'), 'password\r')
  assert.equal(filter.hasPending(), false)
})

test('serial input suppresses a cursor-position reply at every split boundary', () => {
  const reply = '\x1b[29;99R'
  for (let split = 1; split < reply.length; split += 1) {
    const filter = createSerialConsoleInputFilter()
    assert.equal(filter.filter(reply.slice(0, split)), '', `split ${split} first chunk`)
    assert.equal(filter.filter(`${reply.slice(split)}password\r`), 'password\r', `split ${split} second chunk`)
  }
})

test('serial input preserves ordinary terminal escape sequences', () => {
  const filter = createSerialConsoleInputFilter()
  assert.equal(filter.filter('\x1b[A'), '\x1b[A')
  assert.equal(filter.filter('\x1b[?6n'), '\x1b[?6n')
})

test('serial input flushes a lone Escape without leaking it to a later session', () => {
  const filter = createSerialConsoleInputFilter()
  assert.equal(filter.filter('\x1b'), '')
  assert.equal(filter.flush(), '\x1b')
  assert.equal(filter.hasPending(), false)
  assert.equal(filter.filter('password\r'), 'password\r')
})

test('reset prevents a partial serial reply leaking into a new session', () => {
  const filter = createSerialConsoleInputFilter()
  assert.equal(filter.filter('\x1b[29;'), '')
  filter.reset()
  assert.equal(filter.filter('password\r'), 'password\r')
})
