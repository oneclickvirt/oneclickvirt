import request from '@/utils/request'

// 用户仪表盘相关
export function getUserDashboard() {
  return request({
    url: '/v1/user/dashboard',
    method: 'get'
  })
}

export function getAvailableResources(params) {
  return request({
    url: '/v1/user/resources/available',
    method: 'get',
    params
  })
}

export function claimResource(data) {
  return request({
    url: '/v1/user/resources/claim',
    method: 'post',
    data
  })
}

// 用户实例管理
export function getUserInstances(params) {
  return request({
    url: '/v1/user/instances',
    method: 'get',
    params
  })
}

export function getUserContainers(params) {
  return request({
    url: '/v1/user/containers',
    method: 'get',
    params
  })
}

export function getUserVMs(params) {
  return request({
    url: '/v1/user/vms',
    method: 'get',
    params
  })
}

// 用户端口映射API
export const getUserInstancePorts = (instanceId) => {
  return request({
    url: `/v1/user/instances/${instanceId}/ports`,
    method: 'get'
  })
}

export const getUserPortMappings = (params) => {
  return request({
    url: '/v1/user/port-mappings',
    method: 'get',
    params
  })
}

export function instanceAction(instanceId, action) {
  return request({
    url: `/v1/user/instances/${instanceId}/action`,
    method: 'post',
    data: { action }
  })
}

export function getInstanceDetail(instanceId) {
  return request({
    url: `/v1/user/instances/${instanceId}`,
    method: 'get'
  })
}

export function getInstanceLogs(instanceId, params) {
  return request({
    url: `/v1/user/instances/${instanceId}/logs`,
    method: 'get',
    params
  })
}

export function getUserProfile() {
  return request({
    url: '/v1/user/info',
    method: 'get'
  })
}

export function updateProfile(data) {
  return request({
    url: '/v1/user/profile',
    method: 'put',
    data
  })
}

export function resetPassword() {
  return request({
    url: '/v1/user/reset-password',
    method: 'put',
    data: {} // 空数据，后端会自动生成密码
  })
}

export function resetInstancePassword(instanceId) {
  return request({
    url: `/v1/user/instances/${instanceId}/reset-password`,
    method: 'put',
    data: {} // 空数据，后端会自动生成密码
  })
}

export function createUserContainer(data) {
  return request({
    url: '/v1/user/containers',
    method: 'post',
    data
  })
}

export function controlUserContainer(containerId, action) {
  return request({
    url: `/v1/user/containers/${containerId}/action`,
    method: 'post',
    data: { action }
  })
}

export function deleteUserContainer(containerId) {
  return request({
    url: `/v1/user/containers/${containerId}`,
    method: 'delete'
  })
}

export function createUserVM(data) {
  return request({
    url: '/v1/user/vms',
    method: 'post',
    data
  })
}

export function controlUserVM(vmId, action) {
  return request({
    url: `/v1/user/vms/${vmId}/action`,
    method: 'post',
    data: { action }
  })
}

export function deleteUserVM(vmId) {
  return request({
    url: `/v1/user/vms/${vmId}`,
    method: 'delete'
  })
}

export function getAvailableProviders() {
  return request({
    url: '/v1/user/providers/available',
    method: 'get',
    timeout: 10000  // 10秒超时，因为这个API可能需要资源同步
  })
}

export function updateNickname(data) {
  return request({
    url: '/v1/user/nickname',
    method: 'put',
    data
  })
}

// 用户限制相关API
export function getUserLimits() {
  return request({
    url: '/v1/user/limits',
    method: 'get'
  })
}

// 实例详情
export function getUserInstanceDetail(id) {
  return request({
    url: `/v1/user/instances/${id}`,
    method: 'get'
  })
}

// 实例操作
export function performInstanceAction(data) {
  return request({
    url: '/v1/user/instances/action',
    method: 'post',
    data
  })
}

// 实例监控
export function getInstanceMonitoring(id) {
  return request({
    url: `/v1/user/instances/${id}/monitoring`,
    method: 'get'
  })
}

// 创建实例
export function createInstance(data) {
  return request({
    url: '/v1/user/instances',
    method: 'post',
    data,
    timeout: 10000
  })
}

// 获取系统镜像
export function getSystemImages() {
  return request({
    url: '/v1/user/images',
    method: 'get'
  })
}

// 获取过滤后的镜像（基于节点类型和架构）
export function getFilteredImages(params) {
  return request({
    url: '/v1/user/images/filtered',
    method: 'get',
    params
  })
}

// 获取节点的支持能力（支持的实例类型等）
export function getProviderCapabilities(providerId) {
  return request({
    url: `/v1/user/providers/${providerId}/capabilities`,
    method: 'get'
  })
}

// 获取实例配置选项（包含预定义的规格配置）
export function getInstanceConfig(providerId) {
  const params = {}
  if (providerId) {
    params.provider_id = providerId
  }
  return request({
    url: '/v1/user/instance-config',
    method: 'get',
    params
  })
}

// 获取用户实例类型权限配置
export function getUserInstanceTypePermissions() {
  return request({
    url: '/v1/user/instance-type-permissions',
    method: 'get',
    timeout: 8000  // 8秒超时
  })
}

// 任务管理
export function getUserTasks(params) {
  return request({
    url: '/v1/user/tasks',
    method: 'get',
    params
  })
}

export function cancelUserTask(taskId) {
  return request({
    url: `/v1/user/tasks/${taskId}/cancel`,
    method: 'post'
  })
}

// 流量统计相关API
export function getUserTrafficOverview() {
  return request({
    url: '/v1/user/traffic/overview',
    method: 'get'
  })
}

export function getInstanceTrafficDetail(instanceId) {
  return request({
    url: `/v1/user/traffic/instance/${instanceId}`,
    method: 'get'
  })
}

export function getUserInstancesTrafficSummary() {
  return request({
    url: '/v1/user/traffic/instances',
    method: 'get'
  })
}

export function getTrafficLimitStatus() {
  return request({
    url: '/v1/user/traffic/limit-status',
    method: 'get'
  })
}

export function getInstancePmacctData(instanceId) {
  return request({
    url: `/v1/user/traffic/pmacct/${instanceId}`,
    method: 'get'
  })
}

export function getInstancePmacctSummary(instanceId) {
  return request({
    url: `/v1/user/instances/${instanceId}/pmacct/summary`,
    method: 'get'
  })
}

/**
 * 查询实例pmacct流量数据（同步）
 * @param {number} instanceId 实例ID
 * @returns {Promise}
 */
export function queryInstancePmacctData(instanceId) {
  return request({
    url: `/v1/user/instances/${instanceId}/pmacct/query`,
    method: 'get'
  })
}

// 流量历史数据相关API
export function getUserTrafficHistory(params) {
  return request({
    url: '/v1/user/traffic/history',
    method: 'get',
    params
  })
}

export function getInstanceTrafficHistory(instanceId, params) {
  return request({
    url: `/v1/user/instances/${instanceId}/traffic/history`,
    method: 'get',
    params
  })
}
