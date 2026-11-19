import test from 'node:test'
import assert from 'node:assert/strict'
import { mkdtemp, readFile, writeFile, chmod, rm, stat } from 'node:fs/promises'
import path from 'node:path'
import os from 'node:os'
import { SessionStore } from './dsh-store.mjs'
import { OneClickVirtService, baseAddress, redact } from './dsh-service.mjs'
import { fixture } from './dsh-fixture.mjs'
import { operations, validateArguments, apply, guide } from './dsh-plugin.mjs'
async function setup(t) {
  const directory = await mkdtemp(path.join(os.tmpdir(), 'ocv-dsh-test-')), backend = await fixture()
  const service = new OneClickVirtService({ stateDir: directory, environment: {} })
  t.after(async () => { service.dispose(); await backend.close(); await rm(directory, { recursive: true, force: true }) })
  const login = (options = {}) => service.login({ baseUrl: backend.baseUrl, mode: 'password', username: 'admin', password: 'fixture-password', ...options })
  return { ...backend, directory, service, login }
}
test('password login persists a device token, survives restart and revokes only that token on logout', async t => {
  const f = await setup(t), result = await f.login()
  assert.equal(result.connected, true); assert.equal(result.persistent, true)
  assert(!JSON.stringify(result).includes('device-token'))
  const saved = await readFile(path.join(f.directory, 'session.json'), 'utf8')
  assert(saved.includes('device-token-1')); assert(!saved.includes('fixture-password')); assert(!saved.includes('test-admin-token'))
  if (process.platform !== 'win32') assert.equal((await stat(path.join(f.directory, 'session.json'))).mode & 0o777, 0o600)
  const restarted = new OneClickVirtService({ stateDir: f.directory, environment: {} }); t.after(() => restarted.dispose())
  assert.equal((await restarted.status()).user.username, 'admin')
  await restarted.logout()
  assert(!f.state.tokens.has('device-token-1')); assert(f.state.tokens.has('test-admin-token'))
  const third = new OneClickVirtService({ stateDir: f.directory, environment: {} }); t.after(() => third.dispose())
  assert.equal((await third.status()).connected, false)
  await assert.rejects(f.service.call('list_instances'), { code: 'SESSION_CHANGED' })
})
test('the plugin still accepts legacy success envelopes', async t => {
  const f = await setup(t)
  f.state.successCode = 0
  assert.equal((await f.login()).connected, true)
  assert.equal((await f.service.call('list_instances')).list[0].id, 7)
})
test('temporary outage keeps saved credentials; revoked login clears them', async t => {
  const f = await setup(t); await f.login()
  const restarted = new OneClickVirtService({ stateDir: f.directory, environment: {} }); t.after(() => restarted.dispose())
  f.state.failing = true
  assert.equal((await restarted.status()).connected, false)
  assert((await readFile(path.join(f.directory, 'session.json'), 'utf8')).includes('device-token'))
  f.state.failing = false
  assert.equal((await restarted.status()).connected, true)
  f.state.tokens.delete('device-token-1')
  await assert.rejects(restarted.call('list_instances'), { code: 'UNAUTHORIZED' })
  assert(!(await readFile(path.join(f.directory, 'session.json'), 'utf8')).includes('device-token'))
})
test('existing API token is not revoked; remember=false and explicit sign-out suppress environment fallback', async t => {
  const f = await setup(t)
  await f.login({ mode: 'token', token: 'test-user-token', remember: false })
  assert.equal(f.service.snapshot().persistent, false)
  assert(!(await readFile(path.join(f.directory, 'session.json'), 'utf8')).includes('test-user-token'))
  await f.service.logout(); assert(f.state.tokens.has('test-user-token'))
  const restart = new OneClickVirtService({ stateDir: f.directory, environment: { ONE_CLICK_VIRT_API_URL: f.baseUrl, ONE_CLICK_VIRT_API_TOKEN: 'test-admin-token' } }); t.after(() => restart.dispose())
  assert.equal((await restart.status()).connected, false)
})
test('token limit reports a usable alternative without saving password or a temporary JWT', async t => {
  const f = await setup(t)
  f.state.custom = ({ route, send }) => { if (route === '/user/api-tokens') { send(null, 500, 'token limit'); return true } }
  await assert.rejects(f.login(), { code: 'DEVICE_TOKEN_FAILED' }); assert.equal(f.service.snapshot().connected, false)
  assert.equal(await f.service.store.read(), undefined)
})
test('late login after logout cannot resurrect the session or leave its created device token', async t => {
  const f = await setup(t)
  let release, entered
  const ready = new Promise(resolve => { entered = resolve }), wait = new Promise(resolve => { release = resolve })
  f.state.custom = async ({ route, send }) => {
    if (route === '/user/api-tokens') { entered(); await wait; f.state.tokens.set('device-token-1', 'admin'); send({ id: 1, token: 'device-token-1' }); return true }
  }
  const login = assert.rejects(f.login()); await ready; await f.service.logout(); release()
  await login
  assert.equal(f.service.snapshot().connected, false)
  // An aborted HTTP response can be unknowable. If received, the service
  // revokes it; the tombstone always prevents restoring that credential.
  assert(!(await readFile(path.join(f.directory, 'session.json'), 'utf8')).includes('device-token'))
})
test('all Skill operations use role-specific contracts, filtered catalog, and safe output', async t => {
  const f = await setup(t)
  for (const role of ['admin', 'user']) {
    await f.login({ mode: 'token', token: 'test-' + role + '-token' })
    for (const name of ['list_instances', 'get_system_status', 'list_providers']) assert(await f.service.call(name))
    const detail = await f.service.call('get_instance_detail', { instance_id: 7 })
    assert.equal(detail.password, '[redacted]'); assert.equal(detail.sshKey, '[redacted]'); assert.equal(detail.nested.api_token, '[redacted]')
    const catalog = await f.service.call('catalog', { provider_id: 3, instance_type: 'vm' }); assert.equal(catalog.images[0].id, 9)
    const args = { provider_id: 3, instance_type: 'vm', image: 'debian-12', cpu: 2, memory: 1024, disk: 20, image_id: 9, cpu_id: 'cpu-1', memory_id: 'memory-1', disk_id: 'disk-1', bandwidth_id: 'bandwidth-1' }
    assert.equal((await f.service.call('create_instance', args)).taskId, 'create-task')
    const request = f.state.requests.at(-1)
    assert.equal(request.route, '/' + role + '/instances'); assert.equal(request.method, 'POST')
    if (role === 'user') { assert.equal(request.body.providerId, 3); assert.equal(request.body.cpuId, 'cpu-1'); assert.equal(request.body.cpu, undefined) }
    else { assert.equal(request.body.provider_id, 3); assert.equal(request.body.cpu, 2) }
    for (const name of ['start', 'stop', 'restart', 'delete']) {
      await f.service.call(name + '_instance', { instance_id: 7, confirm: true })
      const request = f.state.requests.at(-1)
      assert.equal(request.method, name === 'delete' && role === 'admin' ? 'DELETE' : 'POST')
      if (role === 'user') assert.deepEqual(request.body, { instanceId: 7, action: name })
    }
    await f.service.call('instance_alias', { instance_id: 7, alias: '' }); assert.equal(f.state.instances[0].alias, '')
    assert.equal((await f.service.call('get_metrics', { instance_id: 7 })).cpuUsage, 12)
    const tasks = await f.service.call('tasks', { instance_id: 7 }); assert.equal(tasks.list.length, 1); assert.equal(tasks.list[0].taskData, '[redacted]')
    assert.equal((await f.service.call('get_instance_logs', { instance_id: 7 })).available, false)
    for (const uri of ['oneclickvirt://instances/list', 'oneclickvirt://providers/list', 'oneclickvirt://system/status', 'oneclickvirt://health/status']) assert(await f.service.call('resource', { uri }))
    if (role === 'admin') {
      assert(await f.service.call('health_check', { provider_id: 3 })); assert(await f.service.call('get_metrics', { provider_id: 3 }))
      assert(await f.service.call('resource', { uri: 'oneclickvirt://config/system' }))
    } else {
      const count = f.state.requests.length
      await assert.rejects(f.service.call('health_check', { provider_id: 3 }), { code: 'FORBIDDEN' })
      await assert.rejects(f.service.call('resource', { uri: 'oneclickvirt://config/system' }), { code: 'FORBIDDEN' })
      assert.equal(f.state.requests.length, count)
    }
    await assert.rejects(f.service.call('delete_instance', { instance_id: 7 }), { code: 'CONFIRM_REQUIRED' })
  }
})
test('headless apply registers tools without connection or credentials services', async t => {
  const f = await setup(t), registered = [], effects = []
  apply({ effect: cb => effects.push(cb()), inject: (services, cb) => { if (services[0] === 'tools') cb({ tools: { register: spec => registered.push(spec) } }) } }, { stateDir: f.directory, environment: { ONE_CLICK_VIRT_API_URL: f.baseUrl, ONE_CLICK_VIRT_API_TOKEN: 'test-user-token' } })
  t.after(() => effects.forEach(cleanup => cleanup?.()))
  assert.equal(registered.length, 17)
  const listed = await registered.find(item => item.name === 'oneclickvirt_list_instances').execute({})
  assert.equal(listed.list[0].id, 7)
  assert(guide().templates.create_ubuntu_vm)
  assert.equal(registered.find(item => item.name === 'oneclickvirt_create_instance').isConcurrencySafe(), false)
})
test('argument and address boundaries reject unexpected URLs, fields and destructive ambiguity', () => {
  assert.equal(baseAddress('https://example.com/api/v1/'), 'https://example.com')
  for (const url of ['file:///etc/passwd', 'https://user:secret@example.com', 'https://example.com?token=secret', 'http://example.com']) assert.throws(() => baseAddress(url))
  assert.equal(baseAddress('http://192.0.2.1', true), 'http://192.0.2.1')
  const schema = operations.find(item => item.name === 'delete_instance').parameters
  for (const args of [{ instance_id: 0, confirm: true }, { instance_id: 7 }, { instance_id: 7, confirm: false }, { instance_id: 7, confirm: true, url: 'http://example.com' }]) assert.throws(() => validateArguments(schema, args))
  assert.deepEqual(redact({ ssh_key: 'private', text: 'prefix abc"def suffix' }, ['abc"def']), { ssh_key: '[redacted]', text: 'prefix [redacted] suffix' })
})
test('fallback store migrates once to official credentials, keeps CAS and recovers a dead process lock', async t => {
  const f = await setup(t); await f.login()
  const record = await f.service.store.read(); let stored
  const provider = { readRecord: async () => stored, modifyRecord: async (_key, mutate) => { const next = await mutate(stored); if (next !== undefined) stored = next; return stored } }
  await f.service.store.attach(provider); assert.equal(stored.payload.token, record.payload.token)
  assert(!(await readFile(path.join(f.directory, 'session.json'), 'utf8')).includes('device-token'))
  await f.service.logout(); assert.equal(stored.payload.signedOut, true)
  // Reattaching must not replace an official logout with an old file login.
  await writeFile(path.join(f.directory, 'session.json'), JSON.stringify(record), { mode: 0o600 })
  await f.service.store.attach(provider); assert.equal(stored.payload.signedOut, true)
  const standalone = new SessionStore(f.directory)
  await writeFile(path.join(f.directory, 'session.lock'), JSON.stringify({ pid: 2147483647 }))
  await standalone.modify(() => ({ kind: 'grant', payload: { signedOut: true } }))
  if (process.platform !== 'win32') { await chmod(f.directory, 0o755); await assert.rejects(standalone.read(), /private/); await chmod(f.directory, 0o700) }
})
test('concurrent fallback modifications preserve compare-and-swap', async t => {
  const f = await setup(t), a = new SessionStore(f.directory), b = new SessionStore(f.directory)
  await a.modify(() => ({ counter: 0 }))
  await Promise.all(Array.from({ length: 20 }, (_, i) => (i % 2 ? a : b).modify(async value => ({ counter: value.counter + 1 }))))
  assert.equal((await a.read()).counter, 20)
})
test('polling during logout cannot rehydrate the record before its tombstone is written', async t => {
  const f = await setup(t); await f.login()
  const original = f.service.store.modify.bind(f.service.store)
  let release, entered
  const ready = new Promise(resolve => { entered = resolve }), wait = new Promise(resolve => { release = resolve })
  f.service.store.modify = async mutate => { entered(); await wait; return original(mutate) }
  const signingOut = f.service.logout(); await ready
  assert.equal((await f.service.status()).connected, false)
  await assert.rejects(f.service.call('list_instances'), { code: 'LOGIN_REQUIRED' })
  release(); await signingOut
  assert.equal((await f.service.status()).connected, false)
})
