import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'
import vm from 'node:vm'
import { createActionLock, createKeyedActionLock } from '../src/utils/actionLock.js'

const deferred = () => {
  let resolve
  const promise = new Promise(yes => { resolve = yes })
  return { promise, resolve }
}
const settle = () => new Promise(resolve => setImmediate(resolve))

function setup(overrides = {}) {
  const source = readFileSync(new URL('../src/view/admin/tasks/composables/useTaskManagement.js', import.meta.url), 'utf8')
    .replace(/^import[^\n]+\n/gm, '').replace('export function ', 'function ')
  const timers = new Map()
  let mounted, unmounted, timerID = 0
  const scope = {
    ref: value => ({ value }), reactive: value => value,
    computed: fn => ({ get value() { return fn() } }),
    onMounted: fn => { mounted = fn }, onUnmounted: fn => { unmounted = fn },
    useI18n: () => ({ t: key => key, te: () => false, locale: { value: 'zh-CN' } }),
    useUserStore: () => ({ userType: 'admin' }),
    createActionLock, createKeyedActionLock,
    ElMessage: { error: () => {}, success: () => {}, warning: () => {} },
    ElMessageBox: { confirm: async () => {} }, console,
    setInterval: fn => { const id = ++timerID; timers.set(id, fn); return id },
    clearInterval: id => timers.delete(id),
    getAdminTasks: async () => ({ data: { list: [], total: 0 } }),
    getTaskOverallStats: async () => ({ code: 200, data: {} }),
    getProviderList: async () => ({ code: 200, data: { list: [] } }),
    getTaskPoolStatus: async () => ({ code: 200, data: { enabled: true } }),
    updateTaskPoolStatus: async () => ({ code: 200, data: { enabled: false } }),
    forceStopTask: async () => {}, cancelUserTaskByAdmin: async () => {},
    ...overrides
  }
  vm.runInNewContext(source + '\nglobalThis.ui = useTaskManagement()', scope)
  return { ...scope.ui, timers, mount: () => mounted(), unmount: () => unmounted() }
}

test('automatic refresh updates task rows with statistics without flashing a loading mask', async () => {
  let calls = 0
  const ui = setup({ getAdminTasks: async () => ({ data: { list: [{ id: 1, status: ++calls === 1 ? 'running' : 'cancelled' }], total: 1 } }) })
  ui.mount()
  await settle()
  assert.equal(ui.tasks.value[0].status, 'running')
  const refresh = [...ui.timers.values()][0]
  refresh()
  assert.equal(ui.loading.value, false)
  await settle()
  assert.equal(ui.tasks.value[0].status, 'cancelled')
  ui.unmount()
  assert.equal(ui.timers.size, 0)
})

test('late statistics cannot replace a newer refresh', async () => {
  const old = deferred(), fresh = deferred()
  let calls = 0
  const ui = setup({ getTaskOverallStats: () => (++calls === 1 ? old.promise : fresh.promise) })
  ui.mount()
  ;[...ui.timers.values()][0]()
  fresh.resolve({ code: 200, data: { runningTasks: 0 } })
  await settle()
  old.resolve({ code: 200, data: { runningTasks: 1 } })
  await settle()
  assert.equal(ui.stats.runningTasks, 0)
  ui.unmount()
})

test('a delayed pool status cannot undo the newly saved switch', async () => {
  const old = deferred()
  let calls = 0
  const ui = setup({ getTaskPoolStatus: () => (++calls === 1 ? old.promise : Promise.resolve({ code: 200, data: { enabled: false } })) })
  const earlier = ui.loadTaskPoolStatus()
  await ui.toggleTaskPool(false)
  old.resolve({ code: 200, data: { enabled: true } })
  await earlier
  assert.equal(ui.poolStatus.enabled, false)
})

test('earlier task detail cannot overwrite a reopened dialog or clear its spinner', async () => {
  const old = deferred(), fresh = deferred()
  const ui = setup({ getAdminTaskDetail: id => (id === 1 ? old.promise : fresh.promise) })
  const earlier = ui.viewTaskDetail({ id: 1 })
  ui.detailDialog.visible = false
  const current = ui.viewTaskDetail({ id: 2 })
  old.resolve({ code: 200, data: { id: 1, progressLogs: 'old' } })
  await earlier
  assert.equal(ui.detailDialog.task.id, 2)
  assert.equal(ui.detailDialog.logsLoading, true)
  fresh.resolve({ code: 200, data: { id: 2, progressLogs: 'new' } })
  await current
  assert.equal(ui.detailDialog.task.progressLogs, 'new')
  assert.equal(ui.detailDialog.logsLoading, false)
})

test('closing force stop dialog retains its owner until closed and duplicate submit calls once', async () => {
  const request = deferred()
  let calls = 0
  const ui = setup({ forceStopTask: () => { calls++; return request.promise } })
  ui.showForceStopDialog({ id: 1 })
  ui.forceStopDialog.visible = false
  ui.showForceStopDialog({ id: 2 })
  assert.equal(ui.forceStopDialog.task.id, 1)
  ui.releaseForceStopDialogLock()
  ui.showForceStopDialog({ id: 2 })
  const first = ui.confirmForceStop(), duplicate = ui.confirmForceStop()
  assert.equal(calls, 1)
  request.resolve()
  await Promise.all([first, duplicate])
  ui.releaseForceStopDialogLock()
  assert.equal(ui.isTaskActionLocked(1), false)
  assert.equal(ui.isTaskActionLocked(2), false)
})

test('rapid cancellation opens one confirmation and sends one mutation', async () => {
  const confirmation = deferred()
  let confirmations = 0, mutations = 0
  const ui = setup({
    ElMessageBox: { confirm: () => { confirmations++; return confirmation.promise } },
    cancelUserTaskByAdmin: async () => { mutations++ }
  })
  const first = ui.cancelTask({ id: 1 }), duplicate = ui.cancelTask({ id: 1 })
  assert.equal(confirmations, 1)
  assert.equal(mutations, 0)
  confirmation.resolve()
  await Promise.all([first, duplicate])
  assert.equal(mutations, 1)
  assert.equal(ui.isTaskActionLocked(1), false)
})

test('a dialog cannot reopen while the older submission is still refreshing', async () => {
  const reload = deferred()
  const ui = setup({ getAdminTasks: () => reload.promise })
  ui.showForceStopDialog({ id: 1 })
  const first = ui.confirmForceStop()
  await settle()
  assert.equal(ui.forceStopDialog.visible, false)
  ui.releaseForceStopDialogLock()
  ui.showForceStopDialog({ id: 2 })
  assert.equal(ui.forceStopDialog.task.id, 1)
  reload.resolve({ data: { list: [], total: 0 } })
  await first
  ui.showForceStopDialog({ id: 2 })
  assert.equal(ui.isTaskActionLocked(2), true)
  ui.releaseForceStopDialogLock()
})

test('unmounted task page ignores pending responses', async () => {
  const old = deferred()
  const ui = setup({ getAdminTasks: () => old.promise })
  const request = ui.loadTasks()
  ui.unmount()
  old.resolve({ data: { list: [{ id: 1 }], total: 1 } })
  await request
  assert.equal(ui.tasks.value.length, 0)
})
