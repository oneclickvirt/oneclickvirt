import test from 'node:test'
import assert from 'node:assert/strict'
import { recoverDynamicImport } from '../src/utils/dynamicImportRecovery.js'

function fixture() {
  const values = new Map()
  let reloads = 0
  const options = {
    entryUrl: 'https://panel.test/js/index-old.js',
    storage: () => ({ getItem: key => values.get(key), setItem: (key, value) => values.set(key, value) }),
    reload: () => reloads++
  }
  return { options, reloads: () => reloads }
}

test('stale chunks reload once across repeated failures, then recover after a new deployment', () => {
  const f = fixture()
  const error = new TypeError('Failed to fetch dynamically imported module: https://panel.test/js/removed.js')
  assert.equal(recoverDynamicImport(error, f.options), true)
  for (let i = 0; i < 4; i++) assert.equal(recoverDynamicImport(error, { ...f.options }), false)
  assert.equal(f.reloads(), 1)
  assert.equal(recoverDynamicImport(error, { ...f.options, entryUrl: 'https://panel.test/js/index-new.js' }), true)
  assert.equal(f.reloads(), 2)
})

test('recognizes Safari and Firefox module failures but does not reload application errors', () => {
  for (const message of ['Importing a module script failed.', 'error loading dynamically imported module']) {
    const f = fixture()
    assert.equal(recoverDynamicImport(new TypeError(message), f.options), true)
  }
  const f = fixture()
  for (const error of [new Error('Network Error'), new Error('Permission denied'), null]) {
    assert.equal(recoverDynamicImport(error, f.options), false)
  }
  assert.equal(f.reloads(), 0)
})

test('storage restrictions and missing entry identity never trigger unsafe reloads', () => {
  const f = fixture()
  const error = new Error('Failed to fetch dynamically imported module')
  assert.equal(recoverDynamicImport(error, { ...f.options, storage: () => { throw new Error('Storage denied') } }), false)
  assert.equal(recoverDynamicImport(error, { ...f.options, entryUrl: '' }), false)
  assert.equal(f.reloads(), 0)
})
