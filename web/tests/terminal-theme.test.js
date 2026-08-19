import test from 'node:test'
import assert from 'node:assert/strict'

import { resolveTerminalTheme } from '../src/utils/terminalTheme.js'

test('SSH terminal falls back to a black background when no CSS variable is present', () => {
  const previousDocument = globalThis.document
  const previousGetComputedStyle = globalThis.getComputedStyle

  globalThis.document = { documentElement: {} }
  globalThis.getComputedStyle = () => ({ getPropertyValue: () => '' })

  try {
    assert.equal(resolveTerminalTheme().background, '#000000')
  } finally {
    globalThis.document = previousDocument
    globalThis.getComputedStyle = previousGetComputedStyle
  }
})
