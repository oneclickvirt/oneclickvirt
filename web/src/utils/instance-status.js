const BUSY_STATUSES = new Set([
  'creating',
  'starting',
  'stopping',
  'restarting',
  'rebuilding',
  'resetting',
  'deleting'
])

const DETAIL_READY_STATUSES = new Set(['running', 'stopped', 'error'])

export function getInstanceStatus(instanceOrStatus) {
  if (typeof instanceOrStatus === 'string') return instanceOrStatus
  return instanceOrStatus?.status || instanceOrStatus?.Status || ''
}

export function isInstanceBusy(instanceOrStatus) {
  return BUSY_STATUSES.has(getInstanceStatus(instanceOrStatus))
}

export function canOpenInstanceDetail(instanceOrStatus) {
  return DETAIL_READY_STATUSES.has(getInstanceStatus(instanceOrStatus))
}

export function getInstanceBusyMessage(instanceOrStatus, statusText = '') {
  const status = statusText || getInstanceStatus(instanceOrStatus) || 'unknown'
  return `实例正在操作进行中（当前状态：${status}），请在任务详情中查看进度`
}

