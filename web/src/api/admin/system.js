import request from '@/utils/request'

// Release metadata and submission validation are bounded by the controller's
// remote lookup timeout. Keep these requests above that bound while leaving
// the normal API timeout short for ordinary admin actions.
const UPDATE_METADATA_TIMEOUT_MS = 30 * 1000
const UPDATE_SUBMISSION_TIMEOUT_MS = 35 * 1000

export const getUpdateInfo = () => request({
  url: '/v1/admin/system/check-updates',
  method: 'get',
  timeout: UPDATE_METADATA_TIMEOUT_MS
})

export const getRollbackVersions = () => request({
  url: '/v1/admin/system/rollback-versions',
  method: 'get',
  timeout: UPDATE_METADATA_TIMEOUT_MS
})

export const startSystemUpdate = (version = '', idempotencyKey = '') => request({
	url: '/v1/admin/system/update',
	method: 'post',
	data: version ? { version } : {},
	headers: idempotencyKey ? { 'Idempotency-Key': idempotencyKey } : undefined,
	timeout: UPDATE_SUBMISSION_TIMEOUT_MS
})

export const startSystemRollback = (version, backupId = '', idempotencyKey = '') => request({
	url: '/v1/admin/system/rollback',
	method: 'post',
	data: backupId ? { version, backupId } : { version },
	headers: idempotencyKey ? { 'Idempotency-Key': idempotencyKey } : undefined,
	timeout: UPDATE_SUBMISSION_TIMEOUT_MS
})

export const restartSystem = (idempotencyKey = '') => request({
	url: '/v1/admin/system/restart',
	method: 'post',
	headers: idempotencyKey ? { 'Idempotency-Key': idempotencyKey } : undefined
})

export const getSystemUpdateStatus = () => request({
  url: '/v1/admin/system/update-status',
  method: 'get'
})
