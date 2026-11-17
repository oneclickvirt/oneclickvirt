import { randomUUID } from 'node:crypto'
import os from 'node:os'
import { SessionStore } from './dsh-store.mjs'

export const VERSION = 'v20261117-090644'
const ADMIN_ROLES = new Set(['admin', 'normal_admin', 'super_admin'])
const secretKey = /(?:password|passwd|privatekey|sshkey|secret|token|apikey|accesskey|authorization|cookie|taskdata)/i
export class VirtError extends Error {
  constructor(code, message, status = 400) { super(message); this.code = code; this.status = status }
}
export function redact(value, secrets = []) {
  if (Array.isArray(value)) return value.map(item => redact(item, secrets))
  if (value && typeof value === 'object') return Object.fromEntries(Object.entries(value).map(([key, item]) => [key, secretKey.test(key.replace(/[_-]/g, '')) ? '[redacted]' : redact(item, secrets)]))
  if (typeof value === 'string') for (const secret of secrets) if (secret) value = value.split(secret).join('[redacted]')
  return value
}
export function baseAddress(value, allowHTTP = false) {
  let url
  try { url = new URL(value) } catch { throw new VirtError('INVALID_URL', 'Enter the OneClickVirt deployment URL') }
  if (url.username || url.password || url.search || url.hash || !['https:', 'http:'].includes(url.protocol)) throw new VirtError('INVALID_URL', 'Use an HTTP(S) deployment URL without credentials, query or fragment')
  if (url.protocol === 'http:' && !['localhost', '127.0.0.1', '[::1]'].includes(url.hostname) && !allowHTTP) throw new VirtError('HTTPS_REQUIRED', 'Use HTTPS or explicitly allow HTTP for this deployment')
  return (url.origin + url.pathname.replace(/\/(?:api(?:\/v1)?)\/?$/, '').replace(/\/+$/, ''))
}
const positive = (value, name) => { if (!Number.isSafeInteger(value) || value < 1) throw new VirtError('INVALID_ARGUMENTS', name + ' must be a positive integer'); return value }
const integer = (value, fallback, maximum) => value === undefined ? fallback : Math.min(positive(value, 'value'), maximum)
const matching = (record, session) => record?.kind === 'grant' && record.payload?.id === session?.id && record.payload?.token === session?.token
const identity = data => {
  const user = data?.user || data
  if (!Number.isSafeInteger(user?.id) || user.id < 1 || typeof user.username !== 'string' || !['user', ...ADMIN_ROLES].includes(user.userType)) throw new VirtError('INVALID_RESPONSE', 'The server did not return a supported OneClickVirt account', 502)
  return { id: user.id, username: user.username, userType: user.userType }
}

export class OneClickVirtService {
  constructor(config = {}) {
    this.store = config.store || new SessionStore(config.stateDir)
    this.fetch = config.fetch || globalThis.fetch
    this.session = null; this.revision = 0; this.restoring = null; this.closed = false; this.signedOut = false
    this.controllers = new Set(); this.connectionError = ''; this.pending = null; this.host = { tools: false, connection: false }
    this.environment = config.environment || process.env
  }
  get admin() { return ADMIN_ROLES.has(this.session?.user.userType) }
  snapshot() {
    const session = this.session
    return { version: VERSION, connected: Boolean(session), baseUrl: session?.baseUrl || '', user: session?.user || null, admin: this.admin, persistent: Boolean(session?.persistent), credentialStore: this.store.kind, connectionError: this.connectionError, host: this.host }
  }
  async status(signal) {
    try { await this.restore(signal) } catch (error) { if (signal?.aborted) throw error; this.connectionError = error.code || 'CONNECTION_FAILED' }
    return this.snapshot()
  }
  async exchange(session, route, method = 'GET', body, signal) {
    if (this.closed) throw new VirtError('CLOSED', 'The plugin has stopped')
    const pathname = route.split('?')[0]
    if (!/^\/[a-zA-Z0-9/_-]+$/.test(pathname) || pathname.includes('..') || pathname.includes('//') || /[\r\n#]/.test(route)) throw new VirtError('INVALID_PATH', 'Invalid API route')
    const controller = new AbortController(); this.controllers.add(controller)
    let response
    try {
      response = await this.fetch(session.baseUrl + '/api/v1' + route, {
        method, redirect: 'error', credentials: 'omit',
        headers: { Accept: 'application/json', ...(body === undefined ? {} : { 'Content-Type': 'application/json' }), ...(session.token ? { Authorization: 'Bearer ' + session.token } : {}) },
        ...(body === undefined ? {} : { body: JSON.stringify(body) }),
        signal: AbortSignal.any([controller.signal, AbortSignal.timeout(45000), ...(signal ? [signal] : [])]),
      })
      const reader = response.body?.getReader(), chunks = []; let length = 0
      if (reader) {
        try { for (;;) { const part = await reader.read(); if (part.done) break; length += part.value.byteLength; if (length > 4 * 1024 * 1024) throw new VirtError('RESPONSE_LIMIT', 'The response exceeds 4 MiB; use smaller pages', 502); chunks.push(part.value) } }
        finally { await reader.cancel().catch(() => {}) }
      }
      let result
      try { result = JSON.parse(Buffer.concat(chunks).toString('utf8')) } catch { throw new VirtError('INVALID_RESPONSE', 'The deployment did not return JSON; check its URL and proxy', 502) }
      if (!response.ok || (typeof result.code === 'number' && result.code !== 0)) {
        const unauthorized = response.status === 401 || result.code === 401
        if (unauthorized) throw new VirtError('UNAUTHORIZED', 'The login has expired or was revoked; sign in again', 401)
        let message = typeof result.msg === 'string' ? result.msg.slice(0, 300) : 'OneClickVirt rejected this operation'
        for (const secret of [session.token, body?.password, body?.token]) if (secret) message = message.split(secret).join('[redacted]')
        throw new VirtError(response.status === 403 ? 'FORBIDDEN' : 'API_ERROR', message, response.status >= 400 ? response.status : 400)
      }
      return { data: Object.hasOwn(result, 'data') ? result.data : result, refreshed: response.headers.get('X-New-Token') || '' }
    } catch (error) {
      if (error instanceof VirtError) throw error
      if (signal?.aborted || controller.signal.aborted) throw new VirtError('CANCELLED', method === 'GET' ? 'Request cancelled' : 'Request cancelled; check tasks before repeating the operation')
      throw new VirtError('CONNECTION_FAILED', method === 'GET' ? 'Cannot reach OneClickVirt; the saved login was kept' : 'The response was interrupted; check tasks before repeating the operation', 502)
    } finally { this.controllers.delete(controller) }
  }
  async persist(session, expected = session) {
    if (!session.persistent) return
    const revision = this.revision
    await this.store.modify(async record => {
      if (this.closed || revision !== this.revision || this.session?.id !== session.id) return undefined
      if (expected && !matching(record, expected)) throw new VirtError('SESSION_CHANGED', 'Login changed in another DSH process; reopen this page')
      return { kind: 'grant', payload: session }
    })
  }
  async restore(signal) {
    if (this.signedOut) return null
    if (this.pending) throw new VirtError('AUTH_IN_PROGRESS', 'Sign-in is still being verified; try again afterwards')
    if (this.session) return this.session
    if (this.restoring) { await this.restoring; return this.session }
    const revision = this.revision
    this.restoring = (async () => {
      const record = await this.store.read(), stored = record?.kind === 'grant' && record.payload
      let candidate = stored && !stored.signedOut && typeof stored.token === 'string' && stored.token ? { ...stored } : null
      if (!candidate && !stored?.signedOut && this.environment.ONE_CLICK_VIRT_API_URL && this.environment.ONE_CLICK_VIRT_API_TOKEN) candidate = { id: randomUUID(), baseUrl: baseAddress(this.environment.ONE_CLICK_VIRT_API_URL, this.environment.ONE_CLICK_VIRT_ALLOW_HTTP === 'true'), token: this.environment.ONE_CLICK_VIRT_API_TOKEN, persistent: false }
      if (!candidate) return
      candidate.baseUrl = baseAddress(candidate.baseUrl, candidate.allowHTTP === true)
      try {
        const result = await this.exchange(candidate, '/user/info', 'GET', undefined, signal)
        if (this.closed || revision !== this.revision || signal?.aborted) return
        const before = { ...candidate }
        if (candidate.persistent && !matching(await this.store.read(), candidate)) throw new VirtError('SESSION_CHANGED', 'Login changed in another DSH process; reopen this page')
        if (this.closed || revision !== this.revision || signal?.aborted) return
        candidate.user = identity(result.data)
        if (result.refreshed) candidate.token = result.refreshed
        this.session = candidate; this.connectionError = ''
        if (result.refreshed) await this.persist(candidate, before)
      } catch (error) {
        if (error.status === 401 && revision === this.revision) await this.store.modify(async current => matching(current, candidate) ? { kind: 'grant', payload: { signedOut: true, id: candidate.id } } : undefined)
        throw error
      }
    })()
    try { await this.restoring } finally { this.restoring = null }
    return this.session
  }
  async login(input, signal) {
    const baseUrl = baseAddress(input.baseUrl, input.allowHTTP === true), revision = ++this.revision
    if (!['password', 'token'].includes(input.mode)) throw new VirtError('INVALID_ARGUMENTS', 'Choose password or API token sign-in')
    const initial = await this.store.read(), previous = this.session || (initial?.kind === 'grant' && !initial.payload?.signedOut ? initial.payload : null)
    const candidate = { id: randomUUID(), baseUrl, allowHTTP: input.allowHTTP === true, token: '', persistent: input.remember !== false }
    this.pending = candidate
    let owned
    try {
      if (input.mode === 'token') {
        if (typeof input.token !== 'string' || !input.token.trim() || input.token.length > 8192 || /\s/.test(input.token.trim())) throw new VirtError('INVALID_ARGUMENTS', 'Enter an API token')
        candidate.token = input.token.trim()
      } else {
        if (typeof input.username !== 'string' || typeof input.password !== 'string' || !input.username || !input.password) throw new VirtError('INVALID_ARGUMENTS', 'Enter your username and password')
        const result = await this.exchange(candidate, '/auth/login', 'POST', { username: input.username, password: input.password, captchaId: input.captchaId || '', captcha: input.captcha || '', loginType: 'username' }, signal)
        if (typeof result.data?.token !== 'string' || !result.data.token) throw new VirtError('INVALID_RESPONSE', 'The server did not return a login token', 502)
        candidate.token = result.data.token
      }
      const verified = await this.exchange(candidate, '/user/info', 'GET', undefined, signal)
      candidate.user = identity(verified.data)
      if (verified.refreshed) candidate.token = verified.refreshed
      if (input.mode === 'password' && candidate.persistent) {
        let result
        try { result = await this.exchange(candidate, '/user/api-tokens', 'POST', { name: 'DSH OneClickVirt · ' + os.hostname().slice(0, 60), expireDays: 0, scopes: ['/api/v1/user', ...(ADMIN_ROLES.has(candidate.user.userType) ? ['/api/v1/admin', '/api/v1/config'] : []), '/api/v1/health'] }, signal) }
        catch (error) { if (error.code === 'API_ERROR') throw new VirtError('DEVICE_TOKEN_FAILED', 'Cannot create a persistent device token (check the account token limit). Use an existing API token, or turn off Remember sign-in.'); throw error }
        if (!result.data?.token || !Number.isSafeInteger(result.data.id)) throw new VirtError('INVALID_RESPONSE', 'Cannot create a device login token; use an existing API token instead', 502)
        owned = { ...candidate, token: result.data.token, ownedTokenId: result.data.id }
        candidate.token = owned.token; candidate.ownedTokenId = owned.ownedTokenId
      }
      if (this.closed || revision !== this.revision || signal?.aborted) throw new VirtError('SESSION_CHANGED', 'The sign-in was cancelled or replaced')
      if (candidate.persistent) await this.store.modify(async record => {
        if (revision !== this.revision || this.closed) throw new VirtError('SESSION_CHANGED', 'The sign-in was cancelled or replaced')
        if (JSON.stringify(record) !== JSON.stringify(initial)) throw new VirtError('SESSION_CHANGED', 'Login changed in another DSH process; try again')
        return { kind: 'grant', payload: candidate }
      })
      else await this.store.modify(async record => {
        if (revision !== this.revision || this.closed || JSON.stringify(record) !== JSON.stringify(initial)) throw new VirtError('SESSION_CHANGED', 'Login changed while signing in; try again')
        return { kind: 'grant', payload: { signedOut: true, id: candidate.id } }
      })
      if (revision !== this.revision || this.closed) throw new VirtError('SESSION_CHANGED', 'The sign-in was cancelled or replaced')
      this.session = candidate; this.signedOut = false; this.connectionError = ''
      if (previous?.ownedTokenId && previous.token !== candidate.token) await this.exchange(previous, '/user/api-tokens/' + previous.ownedTokenId, 'DELETE').catch(() => {})
      return this.snapshot()
    } catch (error) {
      await this.store.modify(async record => matching(record, candidate) ? { kind: 'grant', payload: { signedOut: true, id: candidate.id } } : undefined).catch(() => {})
      if (owned) await this.exchange(owned, '/user/api-tokens/' + owned.ownedTokenId, 'DELETE').catch(() => {})
      throw error
    } finally { if (this.pending?.id === candidate.id) this.pending = null }
  }
  async captcha(input, signal) {
    const result = await this.exchange({ baseUrl: baseAddress(input.baseUrl, input.allowHTTP === true) }, '/auth/captcha', 'GET', undefined, signal)
    const data = result.data || {}, image = data.picPath || data.imageData || ''
    return { captchaId: data.captchaId || '', image: /^data:image\/(?:png|jpeg|gif|webp);base64,[a-zA-Z0-9+/=]+$/.test(image) ? image : '' }
  }
  async logout(signal) {
    ++this.revision
    this.signedOut = true
    const current = this.session, pending = this.pending
    this.pending = null
    this.session = null; this.connectionError = ''
    for (const controller of this.controllers) controller.abort()
    const stored = await this.store.read()
    const saved = stored?.kind === 'grant' && !stored.payload?.signedOut ? stored.payload : null
    const previous = saved?.id === pending?.id ? saved : current || saved
    await this.store.modify(async record => !previous || matching(record, previous) ? { kind: 'grant', payload: { signedOut: true, id: previous?.id || randomUUID() } } : undefined)
    let revoked = true
    if (previous?.ownedTokenId) { try { await this.exchange(previous, '/user/api-tokens/' + previous.ownedTokenId, 'DELETE', undefined, signal) } catch { revoked = false } }
    return { ...this.snapshot(), remoteTokenRevoked: revoked }
  }
  async request(route, method, body, signal, requireAdmin = false) {
    await this.restore(signal)
    const session = this.session, revision = this.revision
    if (!session) throw new VirtError('LOGIN_REQUIRED', 'Sign in from the OneClickVirt page; never send passwords or tokens in chat', 401)
    if (session.persistent && !matching(await this.store.read(), session)) {
      this.session = null; ++this.revision
      throw new VirtError('SESSION_CHANGED', 'Login changed in another DSH process; reopen this page')
    }
    if (requireAdmin && !this.admin) throw new VirtError('FORBIDDEN', 'This operation requires an administrator account', 403)
    try {
      const result = await this.exchange(session, route, method, body, signal)
      if (revision !== this.revision || this.session?.id !== session.id) throw new VirtError('SESSION_CHANGED', 'Login changed while this request was running; refresh before continuing')
      if (result.refreshed && this.session.token === session.token) {
        const next = { ...session, token: result.refreshed }
        this.session = next; await this.persist(next, session)
      }
      this.connectionError = ''
      // Error strings and serialized progress logs are not always structured
      // fields. Also scrub the current credential from all returned strings.
      return redact(result.data, [session.token])
    } catch (error) {
      if (error.status === 401 && revision === this.revision && this.session?.id === session.id && this.session.token === session.token) {
        this.session = null
        await this.store.modify(async record => matching(record, session) ? { kind: 'grant', payload: { signedOut: true, id: session.id } } : undefined)
      }
      throw error
    }
  }
  async call(name, args = {}, signal) {
    await this.restore(signal)
    const area = this.admin ? 'admin' : 'user'
    const id = () => positive(args.instance_id, 'instance_id')
    const query = () => new URLSearchParams({ page: String(integer(args.page, 1, 100000)), pageSize: String(integer(args.pageSize, 20, 100)), ...(args.name ? { name: String(args.name).slice(0, 128) } : {}), ...(args.status ? { status: String(args.status).slice(0, 30) } : {}) }).toString()
    if (name === 'list_instances') return this.request('/' + area + '/instances?' + query(), 'GET', undefined, signal)
    if (name === 'get_instance_detail') return this.request('/' + area + '/instances/' + id(), 'GET', undefined, signal)
    if (name === 'list_providers') return this.request(this.admin ? '/admin/providers?' + query() : '/user/providers/available', 'GET', undefined, signal)
    if (name === 'get_system_status') return this.request('/' + area + '/dashboard', 'GET', undefined, signal)
    if (name === 'health_check') return this.request('/admin/providers/' + positive(args.provider_id, 'provider_id') + '/health-check', 'POST', {}, signal, true)
    if (['start_instance', 'stop_instance', 'restart_instance', 'delete_instance'].includes(name)) {
      const action = name.split('_')[0], instance = id()
      if (action === 'delete' && args.confirm !== true) throw new VirtError('CONFIRM_REQUIRED', 'Confirm the exact instance before deleting it')
      if (this.admin) return this.request('/admin/instances/' + instance + (action === 'delete' ? '' : '/action'), action === 'delete' ? 'DELETE' : 'POST', action === 'delete' ? undefined : { action }, signal)
      return this.request('/user/instances/action', 'POST', { instanceId: instance, action }, signal)
    }
    if (name === 'create_instance') {
      const provider = positive(args.provider_id, 'provider_id')
      if (!['container', 'vm'].includes(args.instance_type)) throw new VirtError('INVALID_ARGUMENTS', 'Choose container or vm')
      if (this.admin) {
        if (typeof args.image !== 'string' || !args.image.trim()) throw new VirtError('INVALID_ARGUMENTS', 'Select an image from the provider catalog')
        return this.request('/admin/instances', 'POST', { provider_id: provider, instance_type: args.instance_type, image: args.image, cpu: positive(args.cpu, 'cpu'), memory: positive(args.memory, 'memory'), disk: positive(args.disk, 'disk'), ...(args.name ? { name: String(args.name).slice(0, 128) } : {}), ...(args.network_type ? { network_type: args.network_type } : {}), ...(args.bandwidth ? { bandwidth: positive(args.bandwidth, 'bandwidth') } : {}), ...(args.user_id ? { userId: positive(args.user_id, 'user_id') } : {}) }, signal)
      }
      for (const key of ['cpu_id', 'memory_id', 'disk_id', 'bandwidth_id']) if (typeof args[key] !== 'string' || !args[key]) throw new VirtError('INVALID_ARGUMENTS', 'Read the instance catalog and select ' + key)
      return this.request('/user/instances', 'POST', { providerId: provider, imageId: positive(args.image_id, 'image_id'), cpuId: args.cpu_id, memoryId: args.memory_id, diskId: args.disk_id, bandwidthId: args.bandwidth_id, description: String(args.description || ''), ...(args.network_type ? { networkType: args.network_type } : {}) }, signal)
    }
    if (name === 'instance_alias') {
      if (typeof args.alias !== 'string' || Array.from(args.alias).length > 128) throw new VirtError('INVALID_ARGUMENTS', 'The alias may contain at most 128 characters')
      return this.request('/' + area + '/instances/' + id() + '/alias', 'PUT', { alias: args.alias }, signal)
    }
    if (name === 'get_metrics') {
      if (args.instance_id !== undefined) return this.request('/' + area + '/instances/' + id() + '/monitoring/resources?hours=' + integer(args.hours, 24, 168), 'GET', undefined, signal)
      if (args.provider_id !== undefined) return this.request('/admin/providers/' + positive(args.provider_id, 'provider_id') + '/monitoring/resources', 'GET', undefined, signal, true)
      return this.call('get_system_status', {}, signal)
    }
    if (name === 'get_instance_logs') {
      const detail = await this.call('get_instance_detail', { instance_id: id() }, signal)
      return { available: false, instance_id: args.instance_id, message: 'Guest log retrieval is not exposed by this OneClickVirt HTTP API. Instance state and task errors are available for troubleshooting.', instance: detail, tasks: await this.call('tasks', { instance_id: args.instance_id }, signal) }
    }
    if (name === 'catalog') {
      const provider = positive(args.provider_id, 'provider_id'), type = args.instance_type || 'container'
      if (!['container', 'vm'].includes(type)) throw new VirtError('INVALID_ARGUMENTS', 'Choose container or vm')
      const images = await this.request('/user/images/filtered?provider_id=' + provider + '&instance_type=' + type, 'GET', undefined, signal)
      const configuration = await this.request('/user/instance-config?provider_id=' + provider, 'GET', undefined, signal)
      return { images, configuration }
    }
    if (name === 'tasks') {
      if (args.instance_id === undefined) return this.request('/' + area + '/tasks?' + query(), 'GET', undefined, signal)
      // The current task API does not accept an instanceId filter. Filter a
      // bounded set locally rather than presenting unrelated tasks as logs.
      const instanceId = id(), list = []; let scanned = 0, total = 0
      for (let page = 1; page <= 10; page++) {
        const result = await this.request('/' + area + '/tasks?page=' + page + '&pageSize=100', 'GET', undefined, signal)
        const rows = Array.isArray(result) ? result : result?.list || []
        total = result?.total ?? rows.length; scanned += rows.length
        list.push(...rows.filter(row => (row.instanceId ?? row.instance_id) === instanceId))
        if (!rows.length || scanned >= total) break
      }
      return { list, total: list.length, scanned, complete: scanned >= total }
    }
    if (name === 'resource') {
      const routes = { 'oneclickvirt://health/status': '/health', 'oneclickvirt://config/system': '/config?scope=admin' }
      const calls = { 'oneclickvirt://instances/list': 'list_instances', 'oneclickvirt://providers/list': 'list_providers', 'oneclickvirt://system/status': 'get_system_status' }
      if (calls[args.uri]) return this.call(calls[args.uri], {}, signal)
      if (routes[args.uri]) return this.request(routes[args.uri], 'GET', undefined, signal, args.uri.endsWith('/system'))
      throw new VirtError('INVALID_ARGUMENTS', 'Unknown OneClickVirt resource')
    }
    throw new VirtError('UNKNOWN_OPERATION', 'Unknown OneClickVirt operation')
  }
  dispose() { this.closed = true; ++this.revision; for (const controller of this.controllers) controller.abort(); this.session = null }
}
