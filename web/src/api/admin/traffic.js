import request from '@/utils/request'

// 流量管理相关API
export const getSystemTrafficOverview = () => {
  return request({
    url: '/v1/admin/traffic/overview',
    method: 'get'
  })
}

export const getProviderTrafficStats = (providerId) => {
  return request({
    url: `/v1/admin/traffic/provider/${providerId}`,
    method: 'get'
  })
}

export const getUserTrafficStats = (userId) => {
  return request({
    url: `/v1/admin/traffic/user/${userId}`,
    method: 'get'
  })
}

export const getAllUsersTrafficRank = (params) => {
  return request({
    url: '/v1/admin/traffic/users/rank',
    method: 'get',
    params
  })
}

export const manageTrafficLimits = (data) => {
  return request({
    url: '/v1/admin/traffic/manage',
    method: 'post',
    data
  })
}

export const batchManageTrafficLimits = (data) => {
  return request({
    url: '/v1/admin/traffic/batch-manage',
    method: 'post',
    data
  })
}

export const batchSyncUserTraffic = (data) => {
  return request({
    url: '/v1/admin/traffic/batch-sync',
    method: 'post',
    data
  })
}

// 流量同步相关API
export const syncInstanceTraffic = (instanceId) => {
  return request({
    url: `/v1/admin/traffic/sync/instance/${instanceId}`,
    method: 'post'
  })
}

export const syncUserTraffic = (userId) => {
  return request({
    url: `/v1/admin/traffic/sync/user/${userId}`,
    method: 'post'
  })
}

export const syncProviderTraffic = (providerId) => {
  return request({
    url: `/v1/admin/traffic/sync/provider/${providerId}`,
    method: 'post'
  })
}

export const syncAllTraffic = () => {
  return request({
    url: '/v1/admin/traffic/sync/all',
    method: 'post'
  })
}

// 清空用户流量记录
export const clearUserTrafficRecords = (userId) => {
  return request({
    url: `/v1/admin/traffic/user/${userId}/clear`,
    method: 'delete'
  })
}
