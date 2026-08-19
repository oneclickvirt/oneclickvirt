const EXTENDED_CLIPBOARD_FORMAT_TEXT = 1
const EXTENDED_CLIPBOARD_ACTION_REQUEST = 1 << 25
const EXTENDED_CLIPBOARD_ACTION_NOTIFY = 1 << 27
const EXTENDED_CLIPBOARD_ACTION_PROVIDE = 1 << 28

/**
 * Classify the clipboard path advertised by the active noVNC client.
 * Standard ClientCutText remains a compatibility fallback because RFB has no
 * acknowledgement that a legacy VNC server accepted the pasted content.
 */
export function getVNCClipboardMode(client) {
  if (!client) return 'unknown'
  if (client.viewOnly) return 'readonly'
  if (typeof client.clipboardPasteFrom !== 'function') return 'unsupported'

  const formats = client._clipboardServerCapabilitiesFormats
  const actions = client._clipboardServerCapabilitiesActions
  const supportsExtendedText = formats?.[EXTENDED_CLIPBOARD_FORMAT_TEXT]
    && (actions?.[EXTENDED_CLIPBOARD_ACTION_REQUEST]
      || actions?.[EXTENDED_CLIPBOARD_ACTION_NOTIFY]
      || actions?.[EXTENDED_CLIPBOARD_ACTION_PROVIDE])

  return supportsExtendedText ? 'extended' : 'standard'
}

/**
 * Native paste events provide text without requiring Clipboard API read
 * permission, which makes this path useful for Safari and restricted embeds.
 */
export function getTextFromVNCPasteEvent(event) {
  const clipboardData = event?.clipboardData
  if (!clipboardData || typeof clipboardData.getData !== 'function') return null

  const text = clipboardData.getData('text/plain')
  return typeof text === 'string' ? text : null
}
