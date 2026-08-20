const graphicalProtocols = new Set(['vnc', 'spice', 'native-console'])

function normalizeProtocol(value) {
  return String(value || '').trim().toLowerCase()
}

function fallbackCapability(protocol, info) {
  return {
    protocol,
    available: info?.available === true,
    repairable: info?.repairable === true,
    repairStatus: info?.repairStatus || '',
    reason: info?.available === true ? '' : (info?.reason || ''),
    nativeURL: info?.nativeURL || '',
    terminal: !graphicalProtocols.has(protocol)
  }
}

// Newer controllers return detailed capabilities. Older compatible
// controllers can expose only `protocol`, or a protocol list without details.
// Preserve explicit per-protocol availability when it exists; the aggregate
// `available` flag must never promote an explicitly unavailable protocol.
export function normalizeConsoleCapabilities(info, unsupportedReason) {
  const next = []
  const knownProtocols = new Set()

  const add = (rawCapability, useFallback = false) => {
    const protocol = normalizeProtocol(useFallback ? rawCapability : rawCapability?.protocol)
    if (!protocol || knownProtocols.has(protocol)) return
    knownProtocols.add(protocol)

    if (useFallback) {
      next.push(fallbackCapability(protocol, info))
      return
    }

    next.push({
      ...rawCapability,
      protocol
    })
  }

  const provided = Array.isArray(info?.capabilities) ? info.capabilities : []
  provided.forEach(capability => add(capability))

  const listedProtocols = Array.isArray(info?.protocols) ? info.protocols : []
  listedProtocols.forEach(protocol => add(protocol, true))
  add(info?.protocol, true)

  return next.length
    ? next
    : [{
        protocol: 'unsupported',
        available: false,
        repairable: false,
        reason: info?.reason || unsupportedReason
      }]
}
