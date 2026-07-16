/**
 * Extract the most useful, user-facing reason from initialization failures.
 *
 * Element Plus rejects Form.validate() with an object whose values are arrays
 * of rule errors, while API failures can be either normalized errors from the
 * request interceptor or raw Axios responses. Keep this parser framework
 * agnostic so it can be tested without mounting the initialization view.
 */
export function getInitErrorMessage(error, fallback = 'Initialization failed') {
  const hasStructuredApiError = Boolean(
    error?.userMessage || error?.details || error?.msg || error?.response?.data
  )
  // Form.validate() returns a field map. Do not mistake Error.message from a
  // normalized API error for a form rule message.
  const validationMessage = hasStructuredApiError
    ? null
    : findValidationMessage(error, new Set(), false)
  if (validationMessage) return validationMessage

  const responseData = error?.response?.data
  const candidates = [
    error?.userMessage,
    error?.details,
    error?.msg,
    responseData?.details,
    responseData?.msg,
    responseData?.message,
    typeof error?.message === 'string' ? error.message : null
  ]

  const message = candidates.find(value => {
    if (typeof value !== 'string') return false
    const normalized = value.trim()
    return normalized && normalized !== '[object Object]'
  })

  return message ? message.trim() : fallback
}

function findValidationMessage(value, seen = new Set(), includeOwnMessage = true) {
  if (!value || typeof value !== 'object' || seen.has(value)) return null
  seen.add(value)

  if (includeOwnMessage && typeof value.message === 'string' && value.message.trim()) {
    return value.message.trim()
  }

  if (Array.isArray(value)) {
    for (const item of value) {
      const message = findValidationMessage(item, seen, true)
      if (message) return message
    }
    return null
  }

  for (const item of Object.values(value)) {
    const message = findValidationMessage(item, seen, true)
    if (message) return message
  }

  return null
}
