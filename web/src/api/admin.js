import request from '@/utils/request'
import { healthCheckRequest, createLongTimeoutRequest } from '@/utils/longTimeoutRequest'

// 创建实例专用请求实例（120秒超时）
const instanceOperationRequest = createLongTimeoutRequest(120000, {
  requestPrefix: 'instance'
})

// 任务管理相关API
export const getAdminTasks = (params) => {
  return request({
    url: '/v1/admin/tasks',
    method: 'get',
    params
  })
}

export const forceStopTask = (data) => {
  return request({
    url: '/v1/admin/tasks/force-stop',
    method: 'post',
    data
  })
}

export const getTaskStats = () => {
  return request({
    url: '/v1/admin/tasks/stats',
    method: 'get'
  })
}

export const getTaskOverallStats = () => {
  return request({
    url: '/v1/admin/tasks/overall-stats',
    method: 'get'
  })
}

export const cancelUserTaskByAdmin = (taskId) => {
  return request({
    url: `/v1/admin/tasks/${taskId}/cancel`,
    method: 'post'
  })
}

// 流量监控任务管理
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

export const getAdminDashboard = () => {
  return request({
    url: '/v1/admin/dashboard',
    method: 'get'
  })
}

export const getUserList = (params) => {
  return request({
    url: '/v1/admin/users',
    method: 'get',
    params
  })
}

export const createUser = (data) => {
  return request({
    url: '/v1/admin/users',
    method: 'post',
    data
  })
}

export const updateUser = (id, data) => {
  return request({
    url: `/v1/admin/users/${id}`,
    method: 'put',
    data
  })
}

export const deleteUser = (id) => {
  return request({
    url: `/v1/admin/users/${id}`,
    method: 'delete'
  })
}

export const resetUserPassword = (id) => {
  return request({
    url: `/v1/admin/users/${id}/reset-password`,
    method: 'put',
    data: {} // 空数据，后端会自动生成密码
  })
}

export const updateUserStatus = (id, status) => {
  return request({
    url: `/v1/admin/users/${id}/status`,
    method: 'put',
    data: { status }
  })
}

export const getProviderList = (params) => {
  return request({
    url: '/v1/admin/providers',
    method: 'get',
    params
  })
}

// 检查Provider名称是否已存在
export const checkProviderNameExists = (name, excludeId = null) => {
  return request({
    url: '/v1/admin/providers/check-name',
    method: 'get',
    params: { name, excludeId }
  })
}

// 检查Provider SSH地址和端口是否已存在
export const checkProviderEndpointExists = (endpoint, sshPort, excludeId = null) => {
  return request({
    url: '/v1/admin/providers/check-endpoint',
    method: 'get',
    params: { endpoint, sshPort, excludeId }
  })
}

export const createProvider = (data) => {
  return request({
    url: '/v1/admin/providers',
    method: 'post',
    data
  })
}

export const updateProvider = (id, data) => {
  return request({
    url: `/v1/admin/providers/${id}`,
    method: 'put',
    data
  })
}

export const deleteProvider = (id, force = false) => {
  return request({
    url: `/v1/admin/providers/${id}`,
    method: 'delete',
    params: {
      force: force
    }
  })
}

export const freezeProvider = (id) => {
  return request({
    url: '/v1/admin/providers/freeze',
    method: 'post',
    data: { id }
  })
}

export const unfreezeProvider = (id, expiresAt) => {
  return request({
    url: '/v1/admin/providers/unfreeze',
    method: 'post',
    data: { id, expiresAt }
  })
}

// 测试SSH连接
export const testSSHConnection = (data) => {
  return request({
    url: '/v1/admin/providers/test-ssh-connection',
    method: 'post',
    data,
    timeout: 120000 // 120秒超时，因为要测试3次连接
  })
}

export const updateProviderStatus = (id, status) => {
  return request({
    url: `/v1/admin/providers/${id}/status`,
    method: 'put',
    data: { status }
  })
}

export const getAllInstances = (params) => {
  return request({
    url: '/v1/admin/instances',
    method: 'get',
    params
  })
}

export const createInstance = (data) => {
  return instanceOperationRequest({
    url: '/v1/admin/instances',
    method: 'post',
    data
  })
}

export const updateInstance = (id, data) => {
  return request({
    url: `/v1/admin/instances/${id}`,
    method: 'put',
    data
  })
}

export const deleteInstance = (id) => {
  return instanceOperationRequest({
    url: `/v1/admin/instances/${id}`,
    method: 'delete'
  })
}

export const adminInstanceAction = (id, action) => {
  return instanceOperationRequest({
    url: `/v1/admin/instances/${id}/action`,
    method: 'post',
    data: { action }
  })
}

export const resetInstancePassword = (id) => {
  return request({
    url: `/v1/admin/instances/${id}/reset-password`,
    method: 'put',
    data: {} // 空数据，后端会自动生成密码
  })
}

export const getSystemConfig = () => {
  return request({
    url: '/v1/admin/config',
    method: 'get'
  })
}

export const updateSystemConfig = (data) => {
  return request({
    url: '/v1/admin/config',
    method: 'put',
    data
  })
}

export const getAnnouncements = (params) => {
  return request({
    url: '/v1/admin/announcements',
    method: 'get',
    params
  })
}

export const createAnnouncement = (data) => {
  return request({
    url: '/v1/admin/announcements',
    method: 'post',
    data
  })
}

export const updateAnnouncement = (id, data) => {
  return request({
    url: `/v1/admin/announcements/${id}`,
    method: 'put',
    data
  })
}

export const deleteAnnouncement = (id) => {
  return request({
    url: `/v1/admin/announcements/${id}`,
    method: 'delete'
  })
}

export const batchDeleteAnnouncements = (ids) => {
  return request({
    url: '/v1/admin/announcements/batch-delete',
    method: 'delete',
    data: { ids }
  })
}

export const batchUpdateAnnouncementStatus = (ids, status) => {
  return request({
    url: '/v1/admin/announcements/batch-status',
    method: 'put',
    data: { ids, status }
  })
}

export const getInviteCodes = (params) => {
  return request({
    url: '/v1/admin/invite-codes',
    method: 'get',
    params
  })
}

export const createInviteCode = (data) => {
  return request({
    url: '/v1/admin/invite-codes',
    method: 'post',
    data
  })
}

export const generateInviteCodes = (data) => {
  return request({
    url: '/v1/admin/invite-codes/generate',
    method: 'post',
    data
  })
}

export const deleteInviteCode = (id) => {
  return request({
    url: `/v1/admin/invite-codes/${id}`,
    method: 'delete'
  })
}

export const batchDeleteInviteCodes = (data) => {
  return request({
    url: '/v1/admin/invite-codes/batch-delete',
    method: 'post',
    data
  })
}

export const exportInviteCodes = (data) => {
  return request({
    url: '/v1/admin/invite-codes/export',
    method: 'get',
    params: data
  })
}

export const getProviderMonitoring = (params) => {
  return request({
    url: '/v1/admin/monitoring/providers',
    method: 'get',
    params
  })
}

// 用户状态相关接口
export const toggleUserStatus = (id, status) => {
  return updateUserStatus(id, status)
}

// 批量操作相关接口
export const batchDeleteUsers = (userIds) => {
  return request({
    url: '/v1/admin/users/batch-delete',
    method: 'post',
    data: { userIds }
  })
}

export const batchUpdateUserStatus = (userIds, status) => {
  return request({
    url: '/v1/admin/users/batch-status',
    method: 'put',
    data: { userIds, status }
  })
}

export const batchUpdateUserLevel = (userIds, level) => {
  return request({
    url: '/v1/admin/users/batch-level',
    method: 'put',
    data: { userIds, level }
  })
}

export const updateUserLevel = (id, level) => {
  return request({
    url: `/v1/admin/users/${id}/level`,
    method: 'put',
    data: { level }
  })
}

// 系统镜像管理API
export const systemImageApi = {
  // 获取系统镜像列表
  getList: (params) => {
    return request({
      url: '/v1/admin/system-images',
      method: 'get',
      params
    })
  },

  // 创建系统镜像
  create: (data) => {
    return request({
      url: '/v1/admin/system-images',
      method: 'post',
      data
    })
  },

  // 更新系统镜像
  update: (id, data) => {
    return request({
      url: `/v1/admin/system-images/${id}`,
      method: 'put',
      data
    })
  },

  // 删除系统镜像
  delete: (id) => {
    return request({
      url: `/v1/admin/system-images/${id}`,
      method: 'delete'
    })
  },

  // 批量删除系统镜像
  batchDelete: (data) => {
    return request({
      url: '/v1/admin/system-images/batch-delete',
      method: 'post',
      data
    })
  },

  // 批量更新状态
  batchUpdateStatus: (data) => {
    return request({
      url: '/v1/admin/system-images/batch-status',
      method: 'put',
      data
    })
  }
}

// Provider证书管理API
export const generateProviderCert = (id) => {
  return request({
    url: `/v1/admin/providers/${id}/generate-cert`,
    method: 'post'
  })
}

export const checkProviderHealth = (id) => {
  return healthCheckRequest({
    url: `/v1/admin/providers/${id}/health-check`,
    method: 'post'
  })
}

export const getProviderStatus = (id) => {
  return request({
    url: `/v1/admin/providers/${id}/status`,
    method: 'get'
  })
}

// 配置任务管理API
export const autoConfigureProvider = (data) => {
  // 使用较长的超时时间（150秒），因为自动配置可能需要一些时间
  const configRequest = createLongTimeoutRequest(150000, {
    requestPrefix: 'autoconfig'
  })
  
  return configRequest({
    url: '/v1/admin/providers/auto-configure',
    method: 'post',
    data
  })
}

export const getConfigurationTasks = (params) => {
  return request({
    url: '/v1/admin/configuration-tasks',
    method: 'get',
    params
  })
}

export const getConfigurationTaskDetail = (id) => {
  return request({
    url: `/v1/admin/configuration-tasks/${id}`,
    method: 'get'
  })
}

export const cancelConfigurationTask = (id) => {
  return request({
    url: `/v1/admin/configuration-tasks/${id}/cancel`,
    method: 'post'
  })
}

// 获取实例类型权限配置
export const getInstanceTypePermissions = () => {
  return request({
    url: '/v1/admin/instance-type-permissions',
    method: 'get'
  })
}

// 更新实例类型权限配置
export const updateInstanceTypePermissions = (data) => {
  return request({
    url: '/v1/admin/instance-type-permissions',
    method: 'put',
    data
  })
}



// 端口映射管理API
export const getPortMappings = (params) => {
  return request({
    url: '/v1/admin/port-mappings',
    method: 'get',
    params
  })
}

// 创建端口映射（仅支持手动添加单个端口，仅支持 LXD/Incus/PVE）
export const createPortMapping = (data) => {
  return request({
    url: '/v1/admin/port-mappings',
    method: 'post',
    data
  })
}

// 删除端口映射（仅支持删除手动添加的端口，区间映射的端口不能删除）
export const deletePortMapping = (id) => {
  return request({
    url: `/v1/admin/port-mappings/${id}`,
    method: 'delete'
  })
}

export const batchDeletePortMappings = (ids) => {
  return request({
    url: '/v1/admin/port-mappings/batch-delete',
    method: 'post',
    data: { ids }
  })
}

// 检查端口可用性
export const checkPortAvailable = (data) => {
  return request({
    url: '/v1/admin/ports/check',
    method: 'post',
    data
  })
}

export const allocatePortsForInstance = (instanceId, data) => {
  return request({
    url: `/admin/instances/${instanceId}/ports`,
    method: 'post',
    data
  })
}

export const getInstancePorts = (instanceId) => {
  return request({
    url: `/admin/instances/${instanceId}/ports`,
    method: 'get'
  })
}

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

// 冻结管理相关API
// 用户过期管理
export const setUserExpiry = (data) => {
  return request({
    url: '/v1/admin/users/set-expiry',
    method: 'post',
    data
  })
}

// Provider冻结管理
export const setProviderExpiry = (data) => {
  return request({
    url: '/v1/admin/providers/set-expiry',
    method: 'post',
    data
  })
}

export const freezeProviderManual = (data) => {
  return request({
    url: '/v1/admin/providers/freeze-manual',
    method: 'post',
    data
  })
}

export const unfreezeProviderManual = (data) => {
  return request({
    url: '/v1/admin/providers/unfreeze-manual',
    method: 'post',
    data
  })
}

// 实例冻结管理
export const setInstanceExpiry = (data) => {
  return request({
    url: '/v1/admin/instances/set-expiry',
    method: 'post',
    data
  })
}

export const freezeInstance = (data) => {
  return request({
    url: '/v1/admin/instances/freeze',
    method: 'post',
    data
  })
}

export const unfreezeInstance = (data) => {
  return request({
    url: '/v1/admin/instances/unfreeze',
    method: 'post',
    data
  })
}
