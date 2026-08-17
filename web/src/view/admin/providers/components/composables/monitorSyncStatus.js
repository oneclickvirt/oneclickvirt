const activeStatuses = new Set(['pending', 'running'])
const failedStatuses = new Set(['failed'])
// `success` was emitted by controllers before the task-status contract was
// normalized. Keep it terminal for upgrade-safe polling of historical tasks.
const completedStatuses = new Set(['completed', 'success'])

function normalizeStatus(status) {
  return String(status || '').trim().toLowerCase()
}

export function isMonitorSyncActive(status) {
  return activeStatuses.has(normalizeStatus(status))
}

export function isMonitorSyncFailed(status) {
  return failedStatuses.has(normalizeStatus(status))
}

export function isMonitorSyncTerminal(status) {
  const normalized = normalizeStatus(status)
  return failedStatuses.has(normalized) || completedStatuses.has(normalized)
}
