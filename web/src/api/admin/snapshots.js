import request from '@/utils/request'

export const snapshotApi = {
  overview: () => request({ url: '/v1/admin/snapshots/overview', method: 'get' }),
  list: (params) => request({ url: '/v1/admin/snapshots', method: 'get', params }),
  listByInstance: (instanceId, params) => request({ url: `/v1/admin/instances/${instanceId}/snapshots`, method: 'get', params }),
  create: (instanceId, data) => request({ url: `/v1/admin/instances/${instanceId}/snapshots`, method: 'post', data }),
  delete: (id) => request({ url: `/v1/admin/snapshots/${id}`, method: 'delete' }),
  restore: (id) => request({ url: `/v1/admin/snapshots/${id}/restore`, method: 'post' }),
  schedules: (params) => request({ url: '/v1/admin/snapshot-schedules', method: 'get', params }),
  createSchedule: (data) => request({ url: '/v1/admin/snapshot-schedules', method: 'post', data }),
  updateSchedule: (id, data) => request({ url: `/v1/admin/snapshot-schedules/${id}`, method: 'put', data }),
  deleteSchedule: (id) => request({ url: `/v1/admin/snapshot-schedules/${id}`, method: 'delete' })
}
