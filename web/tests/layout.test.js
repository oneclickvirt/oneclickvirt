import test from 'node:test'
import assert from 'node:assert/strict'

import { shouldUseSidebarDrawer } from '../src/utils/layout.js'

test('uses a drawer on phones and portrait tablets', () => {
  assert.equal(shouldUseSidebarDrawer(390, 844), true)
  assert.equal(shouldUseSidebarDrawer(768, 1024), true)
  assert.equal(shouldUseSidebarDrawer(1037, 1280), true)
  assert.equal(shouldUseSidebarDrawer(1100, 1366), true)
})

test('keeps the fixed sidebar on landscape tablets and desktops', () => {
  assert.equal(shouldUseSidebarDrawer(1024, 768), false)
  assert.equal(shouldUseSidebarDrawer(1280, 800), false)
  assert.equal(shouldUseSidebarDrawer(1440, 1200), false)
})

test('fails closed for invalid viewport dimensions', () => {
  assert.equal(shouldUseSidebarDrawer(undefined, 800), false)
  assert.equal(shouldUseSidebarDrawer(1024, Number.NaN), false)
})
