import assert from 'node:assert/strict'
import test from 'node:test'
import { createActionLock, createKeyedActionLock } from '../src/utils/actionLock.js'

test('rapid duplicate submit invokes the mutation once and releases after completion', async () => {
  const lock = createActionLock()
  let mutationCalls = 0
  let resolve
  const pending = new Promise(r => { resolve = r })
  const submit = async () => {
    if (!lock.tryAcquire()) return
    try {
      mutationCalls += 1
      return await pending
    } finally {
      lock.release()
    }
  }
  const first = submit()
  const second = submit()
  assert.equal(mutationCalls, 1)
  assert.equal(lock.isLocked(), true)
  assert.equal(await second, undefined)
  resolve('done')
  assert.equal(await first, 'done')
  assert.equal(lock.isLocked(), false)
})

test('keyed locks allow independent rows but reject the same row', () => {
  const lock = createKeyedActionLock()
  assert.equal(lock.tryAcquire(7), true)
  assert.equal(lock.tryAcquire('7'), false)
  assert.equal(lock.tryAcquire(8), true)
  assert.equal(lock.isLocked(7), true)
  lock.release(7)
  assert.equal(lock.tryAcquire('7'), true)
  lock.clear()
  assert.equal(lock.isLocked(7), false)
  assert.equal(lock.isLocked(8), false)
})
