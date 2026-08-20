import request from '@/utils/request'

const normalizedScope = (scope) => (scope === 'admin' || scope === 'share' ? scope : 'user')

const consoleApiPath = ({ scope, instanceId, shareToken }) => {
  switch (normalizedScope(scope)) {
    case 'admin':
      return `/v1/admin/instances/${encodeURIComponent(instanceId)}/console`
    case 'share':
      return `/v1/public/instance-shares/${encodeURIComponent(shareToken || '')}/console`
    default:
      return `/v1/user/instances/${encodeURIComponent(instanceId)}/console`
  }
}

const consoleSocketPath = ({ scope, instanceId, shareToken }) => {
  switch (normalizedScope(scope)) {
    case 'admin':
      return `/api/v1/admin/instances/${encodeURIComponent(instanceId)}/console`
    case 'share':
      return `/api/v1/public/instance-shares/${encodeURIComponent(shareToken || '')}/console`
    default:
      return `/api/v1/user/instances/${encodeURIComponent(instanceId)}/console`
  }
}

const getConsoleWebSocketBase = () => {
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  let host = window.location.host
  if (import.meta.env.MODE === 'development' && import.meta.env.VITE_SERVER_PORT) {
    host = `${window.location.hostname}:${import.meta.env.VITE_SERVER_PORT}`
  }
  return `${protocol}//${host}`
}

const consoleSocketURL = (options, suffix, protocol = '') => {
  const params = []
  if (protocol) params.push(`protocol=${encodeURIComponent(protocol)}`)
  const query = params.length ? `?${params.join('&')}` : ''
  return `${getConsoleWebSocketBase()}${consoleSocketPath(options)}${suffix}${query}`
}

export const getScopedInstanceConsoleInfo = (options) => request({
  url: consoleApiPath(options),
  method: 'get'
})

export const repairScopedInstanceConsole = (options) => request({
  url: `${consoleApiPath(options)}/repair`,
  method: 'post'
})

export const getScopedInstanceConsoleWsUrl = (options, protocol = '') => (
  consoleSocketURL(options, '/ws', protocol)
)

export const getScopedInstanceConsoleTerminalWsUrl = (options, protocol = '') => (
  consoleSocketURL(options, '/terminal/ws', protocol)
)

export const getScopedInstanceConsoleSpiceAssetUrl = (options, asset = 'spice_auto.html') => {
  const socketPath = `${consoleSocketPath(options)}/spice-ws`
  const encodedAsset = String(asset).split('/').map(encodeURIComponent).join('/')
  return `${consoleSocketPath(options)}/spice/${encodedAsset}?path=${encodeURIComponent(socketPath)}`
}
