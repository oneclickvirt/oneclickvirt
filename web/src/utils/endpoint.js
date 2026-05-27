export const extractEndpointHost = (rawValue) => {
  const value = String(rawValue || '').trim()
  if (!value) return ''

  if (value.startsWith('[')) {
    const closingBracket = value.indexOf(']')
    if (closingBracket !== -1) {
      return value.slice(1, closingBracket)
    }
    return value.slice(1)
  }

  try {
    const parsed = new URL(/^[a-z][a-z0-9+.-]*:\/\//i.test(value) ? value : `ssh://${value}`)
    if (parsed.hostname) {
      return parsed.hostname
    }
  } catch {
    const colonCount = (value.match(/:/g) || []).length
    if (colonCount > 1) {
      return value
    }
    if (colonCount === 1) {
      return value.split(':')[0]
    }
  }

  return value
}

export const formatEndpointHostForUrl = (rawValue) => {
  const host = extractEndpointHost(rawValue)
  if (!host) return ''
  return host.includes(':') ? `[${host}]` : host
}