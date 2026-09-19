export const extractEndpointHost = (rawValue) => {
  const value = String(rawValue || '').trim()
  if (!value) return ''

  const stripBrackets = (host) => {
    const normalized = String(host || '').trim()
    if (normalized.startsWith('[') && normalized.endsWith(']')) {
      return normalized.slice(1, -1)
    }
    return normalized
  }

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
      return stripBrackets(parsed.hostname)
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

// Render a host and port as a copyable endpoint. IPv6 literals must be
// bracketed so the final colon cannot be mistaken for part of the address.
export const formatEndpointHostPort = (rawHost, port) => {
  const host = formatEndpointHostForUrl(rawHost)
  if (!host) return ''
  if (port === undefined || port === null || String(port).trim() === '') return host
  return `${host}:${port}`
}
