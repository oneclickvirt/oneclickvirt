// X11 keysyms used by the RFB protocol. Keeping the mapping here avoids
// depending on noVNC's internal CommonJS keymap module at runtime.
const keys = Object.freeze({
  control: Object.freeze({ keysym: 0xffe3, code: 'ControlLeft' }),
  alt: Object.freeze({ keysym: 0xffe9, code: 'AltLeft' }),
  shift: Object.freeze({ keysym: 0xffe1, code: 'ShiftLeft' }),
  meta: Object.freeze({ keysym: 0xffeb, code: 'MetaLeft' }),
  delete: Object.freeze({ keysym: 0xffff, code: 'Delete' }),
  tab: Object.freeze({ keysym: 0xff09, code: 'Tab' }),
  escape: Object.freeze({ keysym: 0xff1b, code: 'Escape' }),
  enter: Object.freeze({ keysym: 0xff0d, code: 'Enter' })
})

const functionKeys = Array.from({ length: 12 }, (_, index) => {
  const number = index + 1
  return Object.freeze({
    keysym: 0xffbe + index,
    code: `F${number}`
  })
})

const shortcut = (id, labelKey, display, sequence) => Object.freeze({
  id,
  labelKey,
  display,
  sequence: Object.freeze(sequence)
})

export const VNC_SHORTCUTS = Object.freeze([
  shortcut('ctrlAltDel', 'user.instanceDetail.vncShortcutCtrlAltDel', 'Ctrl+Alt+Del', [keys.control, keys.alt, keys.delete]),
  shortcut('altTab', 'user.instanceDetail.vncShortcutAltTab', 'Alt+Tab', [keys.alt, keys.tab]),
  shortcut('ctrlShiftEsc', 'user.instanceDetail.vncShortcutCtrlShiftEsc', 'Ctrl+Shift+Esc', [keys.control, keys.shift, keys.escape]),
  shortcut('altF4', 'user.instanceDetail.vncShortcutAltF4', 'Alt+F4', [keys.alt, functionKeys[3]]),
  shortcut('ctrlEsc', 'user.instanceDetail.vncShortcutCtrlEsc', 'Ctrl+Esc', [keys.control, keys.escape]),
  shortcut('meta', 'user.instanceDetail.vncShortcutMeta', 'Win / Meta', [keys.meta]),
  shortcut('escape', 'user.instanceDetail.vncShortcutEscape', 'Esc', [keys.escape]),
  shortcut('tab', 'user.instanceDetail.vncShortcutTab', 'Tab', [keys.tab]),
  shortcut('enter', 'user.instanceDetail.vncShortcutEnter', 'Enter', [keys.enter]),
  ...functionKeys.map((key, index) => shortcut(
    `ctrlAltF${index + 1}`,
    'user.instanceDetail.vncShortcutCtrlAltFunction',
    `Ctrl+Alt+F${index + 1}`,
    [keys.control, keys.alt, key]
  ))
])

const shortcutById = new Map(VNC_SHORTCUTS.map(item => [item.id, item]))

export function findVNCShortcut(id) {
  return shortcutById.get(id) || null
}

export function sendVNCShortcut(rfb, id) {
  const item = findVNCShortcut(id)
  if (!item || !rfb || typeof rfb.sendKey !== 'function') return false

  const pressed = []
  try {
    for (const key of item.sequence) {
      rfb.sendKey(key.keysym, key.code, true)
      pressed.push(key)
    }
  } catch (error) {
    // Release keys already pressed when a transport rejects part of a chord.
    for (const key of pressed.reverse()) {
      try {
        rfb.sendKey(key.keysym, key.code, false)
      } catch {
        // Keep the original transport error.
      }
    }
    throw error
  }

  for (const key of pressed.reverse()) {
    rfb.sendKey(key.keysym, key.code, false)
  }
  return true
}
