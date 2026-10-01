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
  let timerID = 0
  const messages = []
  const scope = {
    defineProps: () => props, defineEmits: () => () => {},
    ref: value => ({ value }),
    computed: fn => ({ get value() { return typeof fn === 'function' ? fn() : fn.get() } }),
    watch: () => {}, onBeforeUnmount: () => {},
    useI18n: () => ({ t: key => key }),
    createActionLock, createRequestGeneration,
    ElMessage: { error: value => messages.push(value), success: () => {} },
    window: { setInterval: fn => { const id = ++timerID; timers.set(id, fn); return id }, clearInterval: id => timers.delete(id) },
    getUpdateInfo: async () => ({ data: {} }), getRollbackVersions: async () => ({ data: {} }),
    ...overrides
  }
  vm.runInNewContext(source + '\nglobalThis.api = { loadInfo, startPolling, stopPolling, invalidateDialog, info, loading, operation, reconnecting }', scope)
  return { ...scope.api, timers, props, messages }
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
