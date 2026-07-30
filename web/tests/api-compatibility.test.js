import test from 'node:test'
import assert from 'node:assert/strict'

import {
  expectedApiContract,
  isApiContractMismatch,
  isUnmatchedApiRoute
} from '../src/utils/apiCompatibilityCore.js'

test('detects the plain Go 404 returned by an older controller', () => {
  assert.equal(isUnmatchedApiRoute({ response: { status: 404, data: '404 page not found\n' } }), true)
})

test('detects the structured controller route diagnostic', () => {
  assert.equal(isUnmatchedApiRoute({
    response: {
      status: 404,
      data: { message: 'API endpoint not found', server_version: 'v1' }
    }
  }), true)
})

test('does not classify normal API errors as a version mismatch', () => {
  assert.equal(isUnmatchedApiRoute({ response: { status: 403, data: { message: 'forbidden' } } }), false)
  assert.equal(isUnmatchedApiRoute({ response: { status: 404, data: { message: 'provider not found' } } }), false)
})

test('detects a missing or different API contract', () => {
  assert.equal(isApiContractMismatch({ data: { apiContract: expectedApiContract } }), false)
  assert.equal(isApiContractMismatch({ data: { apiContract: 'older-contract' } }), true)
  assert.equal(isApiContractMismatch({ data: { version: 'older-controller' } }), true)
})
