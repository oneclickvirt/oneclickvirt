import request from '@/utils/request'
import { createLongTimeoutRequest } from '@/utils/longTimeoutRequest'

// 流量监控任务管理（pmacct 旧接口，保留兼容）
export const trafficMonitorOperation = (data) => {
  return request({
    url: '/v1/admin/providers/traffic-monitor',
    method: 'post',
    data
  })
}

export const getTrafficMonitorTasks = (providerId, params) => {
  return request({
    url: '/v1/admin/providers/traffic-monitor/tasks',
    method: 'get',
    params: {
      ...params,
      providerId
    }
  })
}

export const getTrafficMonitorTaskDetail = (taskId) => {
  return request({
    url: `/v1/admin/providers/traffic-monitor/tasks/${taskId}`,
    method: 'get'
  })
}

export const getLatestTrafficMonitorTask = (providerId) => {
  return request({
    url: '/v1/admin/providers/traffic-monitor/latest',
    method: 'get',
    params: {
      providerId
    }
  })
}

// 监控管理 - Agent 模式
export const getMonitoringConfig = (providerId) => {
  return request({
    url: `/v1/admin/providers/${providerId}/monitoring/config`,
    method: 'get'
  })
}

export const updateMonitoringConfig = (providerId, data) => {
  return request({
    url: `/v1/admin/providers/${providerId}/monitoring/config`,
    method: 'put',
    data
  })
}

export const deployAgent = (providerId) => {
  return createLongTimeoutRequest(180000)({
    url: `/v1/admin/providers/${providerId}/monitoring/agent`,
    method: 'post'
  })
}

export const uninstallAgent = (providerId) => {
  return createLongTimeoutRequest(120000)({
    url: `/v1/admin/providers/${providerId}/monitoring/agent`,
    method: 'delete'
  })
}

export const getAgentStatus = (providerId) => {
  return request({
    url: `/v1/admin/providers/${providerId}/monitoring/status`,
    method: 'get'
  })
}

export const getProviderMonitors = (providerId, params = {}) => {
  return request({
    url: `/v1/admin/providers/${providerId}/monitoring/monitors`,
    method: 'get',
    params
  })
}

export const syncProviderMonitors = (providerId) => {
  return request({
    url: `/v1/admin/providers/${providerId}/monitoring/sync`,
    method: 'post'
  })
}

export const getProviderMonitorSyncTask = (providerId, taskId) => {
  return request({
    url: `/v1/admin/providers/${providerId}/monitoring/sync/${taskId}`,
    method: 'get'
  })
}

export const getLatestProviderMonitorSyncTask = (providerId) => {
  return request({
    url: `/v1/admin/providers/${providerId}/monitoring/sync/latest`,
    method: 'get'
  })
}

export const clearProviderMonitors = (providerId) => {
  return request({
    url: `/v1/admin/providers/${providerId}/monitoring/clear`,
    method: 'delete'
  })
}

export const listAgentMonitors = (providerId, params = {}) => {
  return request({
    url: `/v1/admin/providers/${providerId}/monitoring/agent-monitors`,
    method: 'get',
    params
  })
}

export const getProviderResources = (providerId) => {
  return request({
    url: `/v1/admin/providers/${providerId}/monitoring/resources`,
    method: 'get'
  })
}

export const getInstanceResources = (instanceId) => {
  return request({
    url: `/v1/admin/instances/${instanceId}/monitoring/resources`,
    method: 'get'
  })
}
