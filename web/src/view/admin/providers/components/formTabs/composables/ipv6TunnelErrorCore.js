function responseData(error) {
  const data = error?.response?.data || error?.rawResponse
  return data && typeof data === 'object' ? data : {}
}

function responseStatus(error, data) {
  return Number(error?.response?.status || error?.status || data.code)
}

function plainResponseText(error) {
  const data = error?.response?.data || error?.rawResponse
  if (typeof data !== 'string') return ''
  const text = data.replace(/\s+/g, ' ').trim()
  if (!text || /<(?:!doctype|html|head|body)\b/i.test(text)) return ''
  return text.slice(0, 1000)
}

// ipv6TunnelErrorMessage prefers structured node diagnostics but still tells an
// operator when a reverse proxy returned an opaque 5xx page instead of JSON.
export function ipv6TunnelErrorMessage(error, fallback, proxyFallback = '') {
  const data = responseData(error)
  const status = responseStatus(error, data)
  const message =
    error?.details ||
    data.details ||
    data.message ||
    data.msg ||
    plainResponseText(error) ||
    error?.serverMessage ||
    (status >= 500 && proxyFallback) ||
    error?.userMessage ||
    error?.message ||
    fallback
  return String(message).trim()
}

export function ipv6TunnelErrorStatus(error) {
  return responseStatus(error, responseData(error))
}
