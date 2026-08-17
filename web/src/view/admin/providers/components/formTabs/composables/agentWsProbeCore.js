// The Agent reverse WebSocket endpoint authenticates during its handshake.
// A browser probe has no provider secret and therefore cannot distinguish a
// healthy endpoint from a rejected anonymous connection.
export function canProbeWssEndpoint(origin) {
  const path = String(origin?.path || '').replace(/\/+$/, '')
  return path !== '/api/v1/ws/agent' && path !== '/ws/agent'
}
