import test from 'node:test'
import assert from 'node:assert/strict'
import { providerNameValidationError as validate } from '../src/utils/providerNameValidation.js'

test('API/imported legacy node names allow unrelated network edits', () => {
  for (const name of ['physical-pve9', 'Old Node', 'legacyname']) {
    assert.equal(validate(name, { isEditing: true, originalName: name }), '')
    assert.notEqual(validate(name), '')
    assert.notEqual(validate(name, { isEditing: false, originalName: name }), '')
  }
})

test('renames still validate format, length and required value', () => {
  const editing = { isEditing: true, originalName: 'physical-pve9' }
  assert.equal(validate('pve9', editing), '')
  assert.equal(validate('other-node', editing), 'serverNamePattern')
  assert.equal(validate('eight888', editing), 'serverNameMaxLength')
  assert.equal(validate('', editing), 'serverNameRequired')
  assert.equal(validate('', { isEditing: true, originalName: '' }), 'serverNameRequired')
})
