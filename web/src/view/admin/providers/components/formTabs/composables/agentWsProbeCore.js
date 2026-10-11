export function extractAgentWsURL(command) {
  const match = String(command || '').match(/--ws-url(?:=|\s+)['"]?(wss?:\/\/[^\s'"]+)/)
  if (!match) return null
  try {
    const url = new URL(match[1])
    return ['ws:', 'wss:'].includes(url.protocol) ? url : null
  } catch { return null }
}

export function agentWsProbeURL(url, protocol) {
  const probe = new URL(url.href)
  probe.protocol = protocol
  // Never send the installation secret, including legacy query secrets.
  probe.search = '?probe=1'
  probe.hash = ''
  return probe.href
}

export function replaceAgentWsProtocol(command, protocol) {
  if (!['ws', 'wss'].includes(protocol)) return String(command || '')
  return String(command || '').replace(
    /(--ws-url(?:=|\s+)['"]?)wss?:\/\//g,
    `$1${protocol}://`
  )
}

export function startAgentWsProbe(url, { Socket = WebSocket, timeoutMs = 4000 } = {}) {
  let socket, timer, finish
  const promise = new Promise(resolve => {
    let settled = false
    finish = available => {
      if (settled) return
      settled = true
      clearTimeout(timer)
      if (socket) {
        socket.onopen = socket.onerror = socket.onclose = null
        socket.close()
      }
      resolve(available)
    }
    try {
      socket = new Socket(url)
      socket.onopen = () => finish(true)
      socket.onerror = socket.onclose = () => finish(false)
      timer = setTimeout(() => finish(false), timeoutMs)
    } catch { finish(false) }
  })
  return { promise, cancel: () => finish(false) }
}
