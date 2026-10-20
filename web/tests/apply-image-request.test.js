import assert from 'node:assert/strict'
import test from 'node:test'
import { createImageRequestGuard } from '../src/utils/applyImageRequest.js'

test('late container responses cannot replace VM images or their error state', async () => {
  let selection = '1:container'
  const guard = createImageRequestGuard(() => selection)
  let completeOld
  const delayed = new Promise(resolve => { completeOld = resolve })
  const currentOld = guard.begin()
  let images = []
  const old = delayed.then(value => { if (currentOld()) images = value })
  selection = '1:vm'
  guard.invalidate()
  const currentNew = guard.begin()
  if (currentNew()) images = ['debian13-vm']
  completeOld(['debian12-container'])
  await old
  assert.deepEqual(images, ['debian13-vm'])
  assert.equal(currentOld(), false)
  assert.equal(currentNew(), true)
})

test('selection changes invalidate outstanding work before another request starts', () => {
  let selection = '1:container'
  const guard = createImageRequestGuard(() => selection)
  const old = guard.begin()
  selection = '2:container'
  assert.equal(old(), false)
  guard.invalidate()
  selection = '1:container'
  assert.equal(old(), false)
  const current = guard.begin()
  guard.invalidate()
  assert.equal(current(), false)
})
