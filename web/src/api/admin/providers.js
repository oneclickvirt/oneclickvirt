import request from '@/utils/request'
import { healthCheckRequest, createLongTimeoutRequest } from '@/utils/longTimeoutRequest'

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

export const getProviderDetail = (id) => {
  return request({
    url: `/v1/admin/providers/${id}`,
    method: 'get'
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

// GPU 检测
export const detectProviderGPUs = (providerId) => {
  return request({
    url: `/v1/admin/providers/${providerId}/detect-gpus`,
    method: 'get',
    timeout: 120000 // 2分钟超时，适应慢速节点
  })
}

// 获取节点上已停止的容器列表（用于复制模式）
export const getStoppedContainers = (providerId) => {
  return request({
    url: `/v1/admin/providers/${providerId}/stopped-containers`,
    method: 'get',
    timeout: 120000 // 2分钟超时，适应慢速节点
  })
}

// 生成 Agent 密钥（内网穿透模式）
export const generateAgentSecret = (providerId) => {
  return request({
    url: `/v1/admin/providers/${providerId}/agent-secret`,
    method: 'post',
    timeout: 15000
  })
}

// 在 Provider 节点上执行命令（SSH 或 Agent 模式）
export const execOnProvider = (providerId, command, timeout = 30) => {
  return request({
    url: `/v1/admin/providers/${providerId}/exec`,
    method: 'post',
    data: { command, timeout },
    timeout: (timeout + 5) * 1000
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

// IPv4地址池管理
export const getProviderIPv4Pool = (providerId, params) => {
  return request({
    url: `/v1/admin/providers/${providerId}/ipv4-pool`,
    method: 'get',
    params
  })
}

export const setProviderIPv4Pool = (providerId, data) => {
  return request({
    url: `/v1/admin/providers/${providerId}/ipv4-pool`,
    method: 'post',
    data
  })
}

export const clearProviderIPv4Pool = (providerId) => {
  return request({
    url: `/v1/admin/providers/${providerId}/ipv4-pool`,
    method: 'delete'
  })
}

export const deleteProviderIPv4PoolEntry = (providerId, entryId) => {
  return request({
    url: `/v1/admin/providers/${providerId}/ipv4-pool/${entryId}`,
    method: 'delete'
  })
}

// 硬件报告
export const saveHardwareReport = (providerId, pasteUrl) => {
  return request({
    url: `/v1/admin/providers/${providerId}/hardware-report`,
    method: 'post',
    data: { pasteUrl }
  })
}

export const getHardwareTestReport = (providerId) => {
  return request({
    url: `/v1/admin/providers/${providerId}/hardware-report`,
    method: 'get'
  })
}

export const deleteHardwareReport = (providerId) => {
  return request({
    url: `/v1/admin/providers/${providerId}/hardware-report`,
    method: 'delete'
  })
}
