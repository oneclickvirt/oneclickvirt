import request from '@/utils/request'
import { createLongTimeoutRequest } from '@/utils/longTimeoutRequest'

// 创建实例专用请求实例（120秒超时）
const instanceOperationRequest = createLongTimeoutRequest(120000, {
  requestPrefix: 'instance'
})

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

export const getAdminInstanceNewPassword = (instanceId, taskId) => {
  return request({
    url: `/v1/admin/instances/${instanceId}/password/${taskId}`,
    method: 'get'
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
