import test from 'node:test'
import assert from 'node:assert/strict'

import {
  VNC_SHORTCUTS,
  findVNCShortcut,
  sendVNCShortcut
} from '../src/utils/vncKeyboard.js'

test('defines unique VNC shortcut commands with usable key sequences', () => {
  const ids = VNC_SHORTCUTS.map(item => item.id)
  assert.equal(new Set(ids).size, ids.length)
  assert.ok(ids.includes('ctrlAltDel'))
  assert.ok(ids.includes('ctrlAltF12'))
  for (const item of VNC_SHORTCUTS) {
    assert.ok(item.sequence.length > 0)
    assert.ok(item.sequence.every(key => Number.isInteger(key.keysym) && key.code))
  }
})

test('sends a chord down in order and releases it in reverse order', () => {
  const events = []
  const rfb = {
    sendKey: (keysym, code, down) => events.push({ keysym, code, down })
  }

  assert.equal(sendVNCShortcut(rfb, 'ctrlAltDel'), true)
  const shortcut = findVNCShortcut('ctrlAltDel')
  assert.deepEqual(
    events,
    [
      ...shortcut.sequence.map(key => ({ ...key, down: true })),
      ...[...shortcut.sequence].reverse().map(key => ({ ...key, down: false }))
    ]
  )
})

test('does not send unknown shortcuts or when the RFB session is unavailable', () => {
  let calls = 0
  const rfb = { sendKey: () => { calls += 1 } }
  assert.equal(sendVNCShortcut(rfb, 'missing'), false)
  assert.equal(sendVNCShortcut(null, 'ctrlAltDel'), false)
  assert.equal(calls, 0)
})

test('releases already pressed keys when a transport error occurs', () => {
  const events = []
  const rfb = {
    sendKey: (keysym, code, down) => {
      events.push({ keysym, code, down })
      if (events.length === 2) throw new Error('socket closed')
    }
  }

  assert.throws(() => sendVNCShortcut(rfb, 'altTab'), /socket closed/)
  assert.deepEqual(events, [
    { keysym: 0xffe9, code: 'AltLeft', down: true },
    { keysym: 0xff09, code: 'Tab', down: true },
    { keysym: 0xffe9, code: 'AltLeft', down: false }
  ])
})
