import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { getTextFromVNCPasteEvent, getVNCClipboardMode } from '../src/utils/vncClipboard.js'

const component = readFileSync(
  resolve(process.cwd(), 'src/components/VNCDialog.vue'),
  'utf8'
)
const toolbar = readFileSync(
  resolve(process.cwd(), 'src/components/VNCShortcutToolbar.vue'),
  'utf8'
)

test('VNC keeps clipboard traffic on the standard noVNC RFB channel', () => {
  assert.match(component, /addEventListener\('clipboard', handleRemoteClipboard\)/)
  assert.match(component, /clipboardPasteFrom\(text\)/)
  assert.match(component, /@clipboard-paste="pasteToRemote"/)
  assert.match(component, /@clipboard-copy="copyRemoteClipboard"/)
  assert.match(component, /getVNCClipboardMode/)
  assert.match(component, /vncClipboardModeStandard/)
  assert.match(component, /vncClipboardReadOnly/)
})

test('VNC exposes both local-to-remote and remote-to-local clipboard actions', () => {
  assert.match(toolbar, /vncClipboardPaste/)
  assert.match(toolbar, /vncClipboardCopy/)
  assert.match(toolbar, /remoteClipboardAvailable/)
})

test('VNC distinguishes standard, extended, read-only, and unavailable clipboard paths', () => {
  assert.equal(getVNCClipboardMode(null), 'unknown')
  assert.equal(getVNCClipboardMode({ viewOnly: true }), 'readonly')
  assert.equal(getVNCClipboardMode({ viewOnly: false }), 'unsupported')
  assert.equal(getVNCClipboardMode({
    viewOnly: false,
    clipboardPasteFrom: () => {},
    _clipboardServerCapabilitiesFormats: {},
    _clipboardServerCapabilitiesActions: {}
  }), 'standard')
  assert.equal(getVNCClipboardMode({
    viewOnly: false,
    clipboardPasteFrom: () => {},
    _clipboardServerCapabilitiesFormats: { 1: true },
    _clipboardServerCapabilitiesActions: { [1 << 27]: true }
  }), 'extended')
})

test('VNC accepts a native paste event when Clipboard API reads are unavailable', () => {
  assert.equal(getTextFromVNCPasteEvent({
    clipboardData: { getData: type => type === 'text/plain' ? 'from native paste' : '' }
  }), 'from native paste')
  assert.equal(getTextFromVNCPasteEvent({}), null)
})
