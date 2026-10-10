import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'
import vm from 'node:vm'
import { createActionLock } from '../src/utils/actionLock.js'
import { createRequestGeneration } from '../src/utils/requestGeneration.js'

const deferred = () => {
  let resolve, reject
  const promise = new Promise((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}

// Execute the component's own setup functions with deterministic API/timer
// dependencies. This exercises delayed replies without a browser or server.
function setup(overrides = {}) {
  const source = readFileSync(new URL('../src/view/layout/components/SystemUpdateDialog.vue', import.meta.url), 'utf8')
    .split('<script setup>')[1].split('</script>')[0].replace(/^import[\s\S]*? from ['"][^'"]+['"]\n/gm, '')
  const timers = new Map()
  const props = { modelValue: true }
  let timerID = 0, unmount
  const messages = [], successes = []
  const scope = {
    defineProps: () => props, defineEmits: () => () => {},
    ref: value => ({ value }),
    computed: fn => ({ get value() { return typeof fn === 'function' ? fn() : fn.get() } }),
    watch: () => {}, onBeforeUnmount: fn => { unmount = fn },
    useI18n: () => ({ t: key => key }),
    createActionLock, createRequestGeneration,
    ElMessage: { error: value => messages.push(value), success: value => successes.push(value) },
    ElMessageBox: { confirm: async () => {} },
    window: { setInterval: fn => { const id = ++timerID; timers.set(id, fn); return id }, clearInterval: id => timers.delete(id) },
    getUpdateInfo: async () => ({ data: {} }), getRollbackVersions: async () => ({ data: {} }),
    ...overrides
  }
  vm.runInNewContext(source + '\nglobalThis.api = { loadInfo, startPolling, stopPolling, invalidateDialog, submitUpdate, submitRollback, submitRestart, selectedUpdateVersion, selectedRollback, info, loading, actionLoading, operation, reconnecting }', scope)
  return { ...scope.api, timers, props, messages, successes, unmount: () => unmount() }
}

test('late poll completion cannot overwrite a newer operation or stop its timer', async () => {
  const old = deferred()
  const ui = setup({ getSystemUpdateStatus: () => old.promise })
  ui.operation.value = { id: 'old', status: 'applying' }
  ui.startPolling()
  const request = [...ui.timers.values()][0]()
  ui.stopPolling()
  ui.operation.value = { id: 'new', status: 'scheduled' }
  ui.startPolling()
  old.resolve({ data: { id: 'old', status: 'succeeded' } })
  await request
  assert.equal(ui.operation.value.id, 'new')
  assert.equal(ui.operation.value.status, 'scheduled')
  assert.equal(ui.timers.size, 1)
})

test('late failed request after close does not show a reconnect warning', async () => {
  const old = deferred()
  const ui = setup({ getSystemUpdateStatus: () => old.promise })
  ui.operation.value = { id: 'old', status: 'applying' }
  ui.startPolling()
  const request = [...ui.timers.values()][0]()
  ui.props.modelValue = false
  ui.invalidateDialog()
  old.reject(new Error('disconnected'))
  await request
  assert.equal(ui.reconnecting.value, false)
  assert.equal(ui.timers.size, 0)
})

test('metadata from an earlier dialog cannot replace a new load or clear its spinner', async () => {
  const old = deferred(), fresh = deferred()
  let calls = 0
  const ui = setup({ getUpdateInfo: () => (++calls === 1 ? old.promise : fresh.promise) })
  const earlier = ui.loadInfo()
  ui.invalidateDialog()
  const current = ui.loadInfo()
  old.resolve({ data: { currentVersion: 'old' } })
  await earlier
  assert.equal(ui.loading.value, true)
  assert.equal(ui.info.value.currentVersion, undefined)
  fresh.resolve({ data: { currentVersion: 'new' } })
  await current
  assert.equal(ui.info.value.currentVersion, 'new')
  assert.equal(ui.loading.value, false)
})

const configureUpdate = ui => {
  ui.info.value = { capability: { canUpdate: true }, releases: [
    { tag: 'v2026.10.01', canUpdate: true }, { tag: 'v2026.10.02', canUpdate: true }
  ] }
  ui.selectedUpdateVersion.value = 'v2026.10.01'
}

test('rapid update and restart clicks submit once and keep the confirmed version', async () => {
  const confirmation = deferred(), calls = []
  let prompts = 0
  const ui = setup({
    ElMessageBox: { confirm: () => { prompts++; return confirmation.promise } },
    startSystemUpdate: async (...args) => { calls.push(args); return { data: { id: 'update-1', status: 'scheduled' } } },
    restartSystem: async () => { throw new Error('duplicate mutation') }
  })
  configureUpdate(ui)
  const first = ui.submitUpdate(), duplicate = ui.submitUpdate(), restart = ui.submitRestart()
  assert.equal(prompts, 1)
  assert.equal(calls.length, 0)
  ui.selectedUpdateVersion.value = 'v2026.10.02'
  confirmation.resolve()
  await Promise.all([first, duplicate, restart])
  assert.equal(calls.length, 1)
  assert.equal(calls[0][0], 'v2026.10.01')
  assert.match(calls[0][1], /^system-.+/)
  assert.equal(ui.operation.value.id, 'update-1')
  assert.equal(ui.timers.size, 1)
  ui.unmount()
  assert.equal(ui.timers.size, 0)
})

test('rollback sends the confirmed backup identity even when the selection changes', async () => {
  const confirmation = deferred(), calls = []
  const ui = setup({
    ElMessageBox: { confirm: () => confirmation.promise },
    startSystemRollback: async (...args) => { calls.push(args); return { data: { id: 'rollback-1', status: 'scheduled' } } }
  })
  ui.selectedRollback.value = { version: 'v2026.09.30', backupId: 'backup-first', canApply: true }
  const request = ui.submitRollback()
  ui.selectedRollback.value = { version: 'v2026.10.01', backupId: 'backup-second', canApply: true }
  confirmation.resolve()
  await request
  assert.deepEqual(calls[0].slice(0, 2), ['v2026.09.30', 'backup-first'])
  assert.match(calls[0][2], /^system-.+/)
  ui.unmount()
})

test('cancelled confirmation releases the lock and the next submit works', async () => {
  let prompts = 0, calls = 0
  const ui = setup({
    ElMessageBox: { confirm: async () => { if (++prompts === 1) throw new Error('cancel'); } },
    startSystemUpdate: async () => { calls++; return { data: { id: 'update-1', status: 'scheduled' } } }
  })
  configureUpdate(ui)
  await ui.submitUpdate()
  assert.equal(calls, 0)
  assert.equal(ui.actionLoading.value, false)
  await ui.submitUpdate()
  assert.equal(calls, 1)
  ui.unmount()
})

test('closing while the confirmation is pending prevents the mutation', async () => {
  const confirmation = deferred()
  let calls = 0
  const ui = setup({
    ElMessageBox: { confirm: () => confirmation.promise },
    startSystemUpdate: async () => { calls++; return { data: {} } }
  })
  configureUpdate(ui)
  const request = ui.submitUpdate()
  ui.props.modelValue = false
  ui.unmount()
  confirmation.resolve()
  await request
  assert.equal(calls, 0)
  assert.equal(ui.actionLoading.value, false)
  assert.equal(ui.timers.size, 0)
})

test('unmounted dialog ignores a late mutation response and does not resume polling', async () => {
  const response = deferred()
  const ui = setup({ startSystemUpdate: () => response.promise })
  configureUpdate(ui)
  const request = ui.submitUpdate()
  await new Promise(resolve => setImmediate(resolve))
  ui.props.modelValue = false
  ui.unmount()
  response.resolve({ data: { id: 'old', status: 'scheduled' } })
  await request
  assert.equal(ui.operation.value, null)
  assert.equal(ui.successes.length, 0)
  assert.equal(ui.timers.size, 0)
})
