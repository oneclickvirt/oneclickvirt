import test from 'node:test'
import assert from 'node:assert/strict'
import { applyDetectedDatabaseType, databaseRequestMatches, fillDatabasePort } from '../src/view/init/databaseDetection.js'

test('detected engine changes only the label in either direction', () => {
  for (const type of ['mysql', 'mariadb']) {
    const form = { type, host: '2001:db8::1', port: '3307', password: ' literal@:/? ' }
    const previous = { ...form }
    const actual = type === 'mysql' ? 'mariadb' : 'mysql'
    assert.equal(applyDetectedDatabaseType(form, { type: actual }), true)
    assert.deepEqual(form, { ...previous, type: actual })
    fillDatabasePort(form, actual)
    assert.equal(form.port, '3307')
  }
})

test('invalid detection is ignored and only an empty port gets a default', () => {
  const form = { type: 'mariadb', port: '' }
  assert.equal(applyDetectedDatabaseType(form, { type: 'unknown' }), false)
  assert.equal(form.type, 'mariadb')
  fillDatabasePort(form, 'mariadb')
  assert.equal(form.port, '3306')
})

test('normalizes a case-mismatched engine reported by a legacy controller', () => {
  const form = { type: 'mysql', port: '3306' }
  assert.equal(applyDetectedDatabaseType(form, { type: ' MariaDB ' }), true)
  assert.equal(form.type, 'mariadb')
})

test('a delayed test result must not change a newer connection form', () => {
  const request = { type: 'mysql', host: 'old-db', port: '3307', username: 'user', password: 'old' }
  assert.equal(databaseRequestMatches({ ...request }, request), true)
  for (const key of Object.keys(request)) {
    assert.equal(databaseRequestMatches({ ...request, [key]: 'changed' }, request), false)
  }
})
