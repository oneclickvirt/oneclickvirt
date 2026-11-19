// Isolated HTTP fixture. No hypervisor, production endpoint or real token.
import http from 'node:http'
import { once } from 'node:events'
export async function fixture() {
  const state = { requests: [], tokens: new Map([['test-admin-token', 'admin'], ['test-user-token', 'user']]), nextToken: 0, instances: [{ id: 7, name: 'host-original', alias: 'Web VM', status: 'running', cpu: 2, memory: 1024, providerName: 'Lab', canStart: true, canStop: true, canRestart: true, canDelete: true }], successCode: 200, failing: false, custom: null }
  const server = http.createServer(async (req, res) => {
    try {
      const input = []; for await (const data of req) input.push(data)
      const body = input.length ? JSON.parse(Buffer.concat(input)) : undefined
      const url = new URL(req.url, 'http://localhost'), route = url.pathname.replace('/api/v1', ''), token = req.headers.authorization?.replace('Bearer ', '')
      state.requests.push({ route, method: req.method, query: Object.fromEntries(url.searchParams), body, token })
      const send = (data, status = 200, msg = '') => { res.writeHead(status, { 'Content-Type': 'application/json' }); res.end(JSON.stringify({ code: status < 400 ? state.successCode : status, data, msg })) }
      if (state.custom && await state.custom({ req, res, route, token, body, send })) return
      if (state.failing) return send(null, 503, 'temporary outage')
      if (route === '/auth/captcha') return send({ captchaId: 'captcha-1', picPath: 'data:image/png;base64,aW1hZ2U=' })
      if (route === '/auth/login') return body.password === 'fixture-password' ? send({ token: body.username === 'admin' ? 'test-admin-token' : 'test-user-token' }) : send(null, 401, 'bad password')
      const role = state.tokens.get(token)
      if (!role) return send(null, 401, 'revoked')
      if (route.startsWith('/admin/') && role !== 'admin') return send(null, 403, 'admin only')
      if (route === '/user/info') return send({ user: { id: role === 'admin' ? 1 : 2, username: role, userType: role } })
      if (route === '/user/api-tokens' && req.method === 'POST') { const id = ++state.nextToken, value = 'device-token-' + id; state.tokens.set(value, role); return send({ id, token: value }) }
      if (/^\/user\/api-tokens\/\d+$/.test(route) && req.method === 'DELETE') { state.tokens.delete('device-token-' + route.split('/').at(-1)); return send({}) }
      if (/^\/(admin|user)\/instances$/.test(route) && req.method === 'GET') return send({ list: state.instances.filter(row => !url.searchParams.get('name') || row.name.includes(url.searchParams.get('name'))), total: state.instances.length })
      if (/^\/(admin|user)\/instances$/.test(route) && req.method === 'POST') return send({ taskId: 'create-task', status: 'pending' })
      if (/^\/(admin|user)\/instances\/7$/.test(route) && req.method === 'GET') return send({ ...state.instances[0], password: 'guest-password', sshKey: 'private-key', nested: { api_token: 'secret' } })
      if (/^\/(admin|user)\/instances\/7\/alias$/.test(route)) { state.instances[0].alias = body.alias; return send({ alias: body.alias }) }
      if (/^\/admin\/instances\/7$/.test(route) && req.method === 'DELETE') return send({ taskId: 'delete-task' })
      if (/^\/admin\/instances\/7\/action$/.test(route) || route === '/user/instances/action') return send({ taskId: 'power-task' })
      if (route === '/admin/providers' || route === '/user/providers/available') return send({ list: [{ id: 3, name: 'Lab', type: 'incus', status: 'active', sshPassword: 'provider-password' }], total: 1 })
      if (route === '/user/images/filtered') {
        if (url.searchParams.get('provider_id') !== '3' || !['container', 'vm'].includes(url.searchParams.get('instance_type'))) return send(null, 400)
        return send([{ id: 9, name: 'debian-12', architecture: 'amd64', instanceType: url.searchParams.get('instance_type') }])
      }
      if (route === '/user/instance-config') return send(Object.fromEntries(['cpu', 'memory', 'disk', 'bandwidth'].map(key => [key === 'cpu' ? 'cpuSpecs' : key + 'Specs', [{ id: key + '-1', name: key + ' spec' }]])))
      if (/^\/(admin|user)\/tasks$/.test(route)) return send({ list: [{ id: 1, instanceId: 99, errorMessage: 'unrelated' }, { id: 2, instanceId: 7, progress: 60, status: 'running', taskData: '{"password":"hidden"}' }], total: 2 })
      if (route.endsWith('/health-check') || route.endsWith('/monitoring/resources') || route.endsWith('/dashboard') || route === '/health' || route === '/config') return send({ healthy: true, cpuUsage: 12, nested: { token: 'hidden', value: 2 } })
      send(null, 404, 'unimplemented fixture route ' + route)
    } catch { if (!res.headersSent) res.writeHead(500); res.end('{}') }
  })
  server.listen(0, '127.0.0.1'); await once(server, 'listening')
  return { state, baseUrl: 'http://127.0.0.1:' + server.address().port, close: async () => { server.closeAllConnections(); await new Promise(resolve => server.close(resolve)) } }
}
