import assert from 'node:assert/strict'
import path from 'node:path'
import os from 'node:os'
import { mkdtemp, rm } from 'node:fs/promises'
import { createRequire } from 'node:module'
import { pathToFileURL } from 'node:url'
import * as Plugin from '../skills/dsh-plugin.mjs'
import { SESSION_RECORD } from '../skills/dsh-store.mjs'
import { fixture } from '../skills/dsh-fixture.mjs'
const runtime = process.argv[2]
if (!runtime) throw new Error('Pass an isolated official DSH runtime directory containing node_modules')
const require = createRequire(path.join(path.resolve(runtime), 'package.json'))
const load = name => import(pathToFileURL(require.resolve(name)))
const [{ Context }, prompt, tools, executor, credentials] = await Promise.all(['@deepseek-ai/cordis', '@deepseek-ai/dsh-system-prompt', '@deepseek-ai/dsh-tools', '@deepseek-ai/dsh-tool-executor-native', '@deepseek-ai/dsh-credentials-local'].map(load))
const directory = await mkdtemp(path.join(os.tmpdir(), 'ocv-official-dsh-')), backend = await fixture()
let ctx
const boot = async () => {
  ctx = new Context()
  await ctx.plugin(prompt.default); await ctx.plugin(tools.default); await ctx.plugin(executor)
  await ctx.plugin(credentials.default, { dshHome: directory, watch: false })
  await ctx.plugin(Plugin, { stateDir: path.join(directory, 'fallback'), environment: {} })
}
const execute = async (name, args = {}) => {
  const result = await ctx.tools.execute({ calls: [{ type: 'tool-call', id: 'ocv-check', name: 'oneclickvirt_' + name, arguments: JSON.stringify(args) }], signal: new AbortController().signal })
  return result.results[0].result
}
try {
  await boot()
  assert.equal(ctx.tools.schemas().length, 17)
  assert.equal((await execute('guide')).isError, false)
  assert.equal((await execute('list_instances')).isError, true)
  assert.equal(ctx.get('connection'), undefined)
  await ctx.credentials.modifyRecord(SESSION_RECORD, () => ({ kind: 'grant', payload: { id: 'official-device', baseUrl: backend.baseUrl, token: 'test-user-token', persistent: true } }))
  const listed = await execute('list_instances'); assert.equal(listed.isError, false); assert.equal(listed.value.list[0].id, 7)
  assert.equal((await execute('health_check', { provider_id: 3 })).isError, true)
  assert.equal((await execute('delete_instance', { instance_id: 7, confirm: false })).isError, true)
  await ctx.fiber.dispose(); await boot()
  const restored = await execute('get_instance_detail', { instance_id: 7 }); assert.equal(restored.isError, false); assert.equal(restored.value.password, '[redacted]')
  console.log(JSON.stringify({ runtime: require('@deepseek-ai/dsh-tools/package.json').version, nativeTools: ctx.tools.schemas().length, noWebRequired: true, officialCredentialsRestart: true, result: 'passed' }))
} finally { await ctx?.fiber.dispose(); await backend.close(); await rm(directory, { recursive: true, force: true }) }
