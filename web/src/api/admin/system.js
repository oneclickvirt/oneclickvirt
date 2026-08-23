import request from '@/utils/request'

export const getUpdateInfo = () => request({
  url: '/v1/admin/system/check-updates',
  method: 'get'
})

export const getRollbackVersions = () => request({
  url: '/v1/admin/system/rollback-versions',
  method: 'get'
})

export const startSystemUpdate = (version = '') => request({
  url: '/v1/admin/system/update',
  method: 'post',
  data: version ? { version } : {}
})

export const startSystemRollback = (version, backupId = '') => request({
  url: '/v1/admin/system/rollback',
  method: 'post',
  data: backupId ? { version, backupId } : { version }
})

export const restartSystem = () => request({
  url: '/v1/admin/system/restart',
  method: 'post'
})

export const getSystemUpdateStatus = () => request({
  url: '/v1/admin/system/update-status',
  method: 'get'
})
